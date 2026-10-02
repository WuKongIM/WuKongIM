package mqtt

import (
	"context"
	"errors"
	"time"

	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
)

var (
	ErrPublisherInvalid = errors.New("mqtt: invalid publisher configuration or connection")
	ErrPublishUncertain = errors.New("mqtt: publication outcome unavailable")
	ErrPublishRejected  = errors.New("mqtt: publication rejected")
	ErrPublishPanic     = errors.New("mqtt: publication callback failed")
)

// PublishMessages is implemented by the existing message usecase. The permission
// query must use current authority and accept only ordinary person/group targets.
// Send owns hooks, directory creation, routing and committed idempotency. An
// uncertain return may leave accepted Channel work running; it is not drain proof.
type PublishMessages interface {
	CheckPublishPermission(context.Context, message.PublishPermissionQuery) (message.Reason, error)
	Send(context.Context, message.SendCommand) (message.SendResult, error)
}

type PublisherOptions struct {
	Owners   *runtime.Owners
	Messages PublishMessages
	// Timeout bounds permission and Send work together, defaulting to five seconds.
	Timeout time.Duration
	// Now supplies the original ingress wall clock, preserved through Send retries.
	Now func() time.Time
}

// Publisher handles only authenticated inbound publications. It owns no listener,
// Session lifecycle, durable QoS tracker, retry queue, or background task.
type Publisher struct{ options PublisherOptions }

func NewPublisher(options PublisherOptions) (*Publisher, error) {
	if options.Timeout == 0 {
		options.Timeout = 5 * time.Second
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Owners == nil || options.Messages == nil || options.Timeout < 0 || options.Timeout > time.Minute {
		return nil, ErrPublisherInvalid
	}
	return &Publisher{options: options}, nil
}

// Publish retains exact-owner execution through the final reply enqueue. The
// gateway must supply the accepted connection descriptor and its own Session,
// and serialize packets using its bounded ordered mailbox. Fatal closure only
// requests lifecycle cleanup; it never waits recursively for this operation.
func (p *Publisher) Publish(gateway gt.Context, connection sessioncase.Connection, packet *wire.Publish) (err error) {
	var op *runtime.Operation
	var uncertainSend bool
	defer func() {
		defer op.Done()
		// The shared append runtime can return while admitted writes continue.
		// Preserve isolation failure before releasing this entry's local scope.
		defer func() {
			if uncertainSend {
				_ = op.MarkUncertain()
			}
		}()
		if recover() != nil {
			err = ErrPublishPanic
			p.close(gateway, connection, err)
		}
	}()
	if p == nil || gateway.Session == nil || gateway.RequestContext == nil || connection.DeviceFlag > 2 {
		p.close(gateway, connection, ErrPublisherInvalid)
		return ErrPublisherInvalid
	}
	deadline := time.Now().Add(p.options.Timeout)
	if parentDeadline, ok := gateway.RequestContext.Deadline(); ok && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}
	op, err = p.options.Owners.Begin(gateway.RequestContext, connection.Owner)
	if err != nil {
		p.close(gateway, connection, err)
		return publishFailure{err}
	}
	// The operation carries owner cancellation; preserve the request's earlier
	// deadline explicitly because owner scopes link parent cancellation by hook.
	ctx, cancel := context.WithDeadline(op.Context(), deadline)
	defer cancel()
	check := func() error {
		if err := gateway.RequestContext.Err(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return op.Check()
	}
	if op.UID() != connection.UID || gateway.Session.ID() == 0 {
		p.close(gateway, connection, ErrPublisherInvalid)
		return ErrPublisherInvalid
	}
	terminate := func(reason byte, cause error) error {
		defer p.close(gateway, connection, cause)
		// Once fenced or cancelled, no further protocol write may be started here.
		if reason != 0 && check() == nil {
			_ = gateway.WritePacket(&wire.Disconnect{Reason: reason})
		}
		return publishFailure{cause}
	}
	if packet == nil || packet.QoS == 1 && packet.PacketID == 0 || packet.QoS == 0 && (packet.PacketID != 0 || packet.Dup) {
		return terminate(wire.ProtocolError, publicationError(wire.ProtocolError))
	}
	input, err := MapPublish(packet, connection.Owner.Key.Namespace, connection.Owner.Key.ClientID, p.options.Now().UnixMilli())
	if err != nil {
		var protocol *wire.Error
		if errors.As(err, &protocol) {
			return terminate(protocol.Reason, err)
		}
		return terminate(wire.ProtocolError, ErrPublisherInvalid)
	}
	reply := func(reason byte) error {
		if err := check(); err != nil {
			return terminate(0, err)
		}
		if packet.QoS == 0 {
			if reason != 0 {
				return terminate(reason, ErrPublishRejected)
			}
			return nil
		}
		if err := gateway.WritePacket(&wire.Puback{PacketID: packet.PacketID, Reason: reason}); err != nil {
			return terminate(0, err)
		}
		return nil
	}
	reject := func(reason message.Reason) error {
		mqttReason, definite := publishRejection(reason)
		if !definite {
			return terminate(0x83, ErrPublishUncertain)
		}
		return reply(mqttReason)
	}
	if err := check(); err != nil {
		return terminate(0, err)
	}
	reason, err := p.options.Messages.CheckPublishPermission(ctx, message.PublishPermissionQuery{FromUID: op.UID(), TargetID: input.Target.ChannelID, TargetType: input.Target.ChannelType})
	if e := check(); e != nil {
		return terminate(0, e)
	}
	if err != nil {
		// This query has no append effects; its invalid-target result is definite.
		if errors.Is(err, message.ErrInvalidCommand) {
			return reply(0x90)
		}
		return terminate(0x83, err)
	}
	if reason != message.ReasonSuccess {
		return reject(reason)
	}
	uncertainSend = true
	result, err := p.options.Messages.Send(ctx, message.SendCommand{
		FromUID: op.UID(), DeviceFlag: uint8(connection.DeviceFlag),
		SenderNodeID: connection.Owner.NodeID, SenderSessionID: gateway.Session.ID(),
		ChannelID: input.Target.ChannelID, ChannelType: input.Target.ChannelType,
		NormalizePersonChannel: input.Target.ChannelType == 1,
		ClientMsgNo:            input.ClientMsgNo, Payload: input.Payload, PublicationMetadata: input.Metadata,
		Origin: message.SendOriginClient,
	})
	if errors.Is(err, message.ErrAppendNotSubmitted) && result.MessageID == 0 && result.MessageSeq == 0 {
		// Whole-invocation proof covers this operation only. It does not clear
		// unresolved work previously retained by another owner operation.
		uncertainSend = false
	}
	if err == nil {
		_, definiteRejection := publishRejection(result.Reason)
		committed := result.Reason == message.ReasonSuccess && result.MessageID != 0 && result.MessageSeq != 0
		rejected := definiteRejection && result.MessageID == 0 && result.MessageSeq == 0
		uncertainSend = !committed && !rejected
	}
	if e := check(); e != nil {
		return terminate(0, e)
	}
	if err != nil {
		return terminate(0x83, err)
	}
	if result.Reason != message.ReasonSuccess {
		if result.MessageID != 0 || result.MessageSeq != 0 {
			return terminate(0x83, ErrPublishUncertain)
		}
		return reject(result.Reason)
	}
	if result.MessageID == 0 || result.MessageSeq == 0 {
		return terminate(0x83, ErrPublishUncertain)
	}
	return reply(0)
}

// close fences future admission before requesting gateway closure. The gateway
// owns physical close and asynchronous lifecycle cleanup; neither is proved here.
func (p *Publisher) close(gateway gt.Context, connection sessioncase.Connection, cause error) {
	if p != nil {
		_ = p.options.Owners.Fence(connection.Owner)
	}
	// Close notification is best-effort; a callback panic cannot reopen admission
	// or prevent the caller's deferred operation release.
	defer func() { _ = recover() }()
	_ = gateway.CloseSession(gt.CloseReasonHandlerError, publishFailure{cause})
}

func publishRejection(reason message.Reason) (byte, bool) {
	switch reason {
	case message.ReasonAuthFail, message.ReasonSubscriberNotExist, message.ReasonInBlacklist,
		message.ReasonNotAllowSend, message.ReasonNotInWhitelist, message.ReasonBan,
		message.ReasonDisband, message.ReasonSendBan:
		return 0x87, true
	case message.ReasonChannelNotExist, message.ReasonInvalidRequest:
		return 0x90, true
	case message.ReasonUnsupported:
		return 0x83, true
	default:
		return 0x83, message.IsBusinessReasonCode(uint32(reason))
	}
}

// Keep dependency causes available for classification without exposing their
// arbitrary text to protocol clients or the gateway's ordinary error logger.
type publishFailure struct{ cause error }

func (publishFailure) Error() string   { return "mqtt: publication failed" }
func (e publishFailure) Unwrap() error { return e.cause }
