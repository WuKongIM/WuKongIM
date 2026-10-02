package message

import (
	"context"
	"errors"
	"fmt"
	"time"

	channelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

// BeforeSendRequest is the final permission-accepted message presented to the
// business callback. ChannelID is the canonical source channel (including generated
// subscriber-scoped channels); no committed message identity exists yet.
type BeforeSendRequest struct {
	FromUID     string
	ChannelID   string
	ChannelType uint8
	ClientMsgNo string
	Payload     []byte
	NoPersist   bool
	SyncOnce    bool
}

// BeforeSendDecision permits a payload replacement or rejects the current send.
// A nil Payload preserves the request; an explicit empty value clears MQTT
// content only. Native messages retain their nonempty-body requirement.
// ReasonCode is an external business code, never the internal Reason enum;
// zero selects the standard rejection.
type BeforeSendDecision struct {
	Allow      bool
	Payload    []byte
	ReasonCode uint32
}

// BeforeSendCaller performs one callback and must honor context cancellation.
// Request payloads are borrowed only until the call returns and must not be mutated.
type BeforeSendCaller interface {
	BeforeSend(context.Context, BeforeSendRequest) (BeforeSendDecision, error)
}

// BeforeSendOptions configures one node's synchronous admission policy.
type BeforeSendOptions struct {
	Caller BeforeSendCaller
	// Timeout bounds the callback within the original send's remaining deadline.
	Timeout time.Duration
	// OnTimeout and OnError are independently "allow" or "deny".
	OnTimeout string
	OnError   string
	// MaxInFlight bounds concurrent callbacks without an additional waiting queue.
	MaxInFlight int
	// MaxPayloadBytes is the product protocol's payload limit.
	MaxPayloadBytes int
	// Observe receives only fixed result names and elapsed time, never identities.
	Observe func(result string, duration time.Duration)
}

// BeforeSendWebhook owns failure policy and bounded admission for a business callback.
type BeforeSendWebhook struct {
	opts BeforeSendOptions
	// active retains one token per running callback; saturation always rejects.
	active chan struct{}
}

// NewBeforeSendWebhook validates the policy before any product traffic is admitted.
func NewBeforeSendWebhook(opts BeforeSendOptions) (*BeforeSendWebhook, error) {
	if opts.Caller == nil || opts.Timeout <= 0 || opts.MaxInFlight <= 0 || opts.MaxPayloadBytes <= 0 {
		return nil, fmt.Errorf("before-send webhook requires caller and positive limits")
	}
	for _, policy := range []string{opts.OnTimeout, opts.OnError} {
		if policy != "allow" && policy != "deny" {
			return nil, fmt.Errorf("before-send webhook policy must be allow or deny")
		}
	}
	return &BeforeSendWebhook{opts: opts, active: make(chan struct{}, opts.MaxInFlight)}, nil
}

// check runs after plugins and before authority routing for every send mode.
// Parent cancellation always wins; a business denial can never fail open.
func (w *BeforeSendWebhook) check(ctx context.Context, cmd SendCommand, commandChannels channelid.CommandCodec) (SendCommand, Reason, error) {
	start := time.Now()
	outcome := "error_deny"
	defer func() {
		if w.opts.Observe != nil {
			w.opts.Observe(outcome, time.Since(start))
		}
	}()
	if err := ctx.Err(); err != nil {
		outcome = "canceled"
		return cmd, ReasonSystemError, err
	}
	select {
	case w.active <- struct{}{}:
		defer func() { <-w.active }()
	default:
		outcome = "overloaded"
		return cmd, ReasonSystemError, nil
	}
	// Only fully validated MQTT provenance permits empty input or replacement.
	allowEmpty := len(cmd.PublicationMetadata) != 0
	if publication.ValidateSend(cmd.PublicationMetadata) != nil || (!allowEmpty && len(cmd.Payload) == 0) || len(cmd.Payload) > w.opts.MaxPayloadBytes {
		outcome = "invalid_request"
		return cmd, ReasonInvalidRequest, nil
	}
	callCtx, cancel := context.WithTimeout(ctx, w.opts.Timeout)
	defer cancel()
	request, err := beforeSendRequest(cmd, commandChannels)
	if err != nil {
		outcome = "invalid_request"
		return cmd, ReasonInvalidRequest, err
	}
	decision, err := w.opts.Caller.BeforeSend(callCtx, request)
	if parentErr := ctx.Err(); parentErr != nil {
		outcome = "canceled"
		return cmd, ReasonSystemError, parentErr
	}
	// A decoded denial remains a denial even if its optional fields are invalid
	// or the callback deadline expires before this goroutine resumes.
	if err == nil && !decision.Allow {
		outcome = "reject"
		if IsBusinessReasonCode(decision.ReasonCode) && decision.Payload == nil {
			return cmd, Reason(decision.ReasonCode), nil
		}
		return cmd, ReasonNotAllowSend, nil
	}
	if callCtx.Err() != nil {
		err = callCtx.Err()
	}
	if err == nil {
		err = w.validateDecision(decision, allowEmpty)
	}
	if err != nil {
		policy := w.opts.OnError
		outcome = "error_"
		var timeout interface{ Timeout() bool }
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
			policy = w.opts.OnTimeout
			outcome = "timeout_"
		}
		outcome += policy
		if policy == "allow" {
			return cmd, ReasonSuccess, nil
		}
		// Callback failures are terminal admission failures, not authority movement.
		return cmd, ReasonSystemError, nil
	}
	outcome = "allow"
	if decision.Payload != nil {
		cmd.Payload = append([]byte(nil), decision.Payload...)
	}
	return cmd, ReasonSuccess, nil
}

func (w *BeforeSendWebhook) validateDecision(d BeforeSendDecision, allowEmpty bool) error {
	if d.Allow {
		if d.ReasonCode != 0 || (d.Payload != nil && ((!allowEmpty && len(d.Payload) == 0) || len(d.Payload) > w.opts.MaxPayloadBytes)) {
			return errors.New("invalid before-send allow decision")
		}
	}
	return nil
}

// IsBusinessReasonCode identifies the reserved external rejection range shared
// by callback validation and protocol adapters, separate from internal Reasons.
func IsBusinessReasonCode(code uint32) bool { return code >= 128 && code <= 255 }

func beforeSendRequest(cmd SendCommand, commandChannels channelid.CommandCodec) (BeforeSendRequest, error) {
	sourceID, _ := commandChannels.FromCommandChannel(cmd.ChannelID)
	channelType := cmd.ChannelType
	if cmd.RequestScoped || (len(cmd.MessageScopedUIDs) > 0 && cmd.ChannelID == "") {
		if !cmd.SyncOnce {
			return BeforeSendRequest{}, ErrRequestSubscribersRequireSyncOnce
		}
		if cmd.ChannelID != "" {
			return BeforeSendRequest{}, ErrRequestSubscribersConflictChannel
		}
		scoped, err := commandChannels.RequestSubscriberChannelFor(cmd.MessageScopedUIDs)
		if err != nil {
			return BeforeSendRequest{}, ErrRequestSubscribersRequired
		}
		sourceID, channelType = scoped.SourceChannelID, scoped.ChannelType
	}
	return BeforeSendRequest{FromUID: cmd.FromUID, ChannelID: sourceID, ChannelType: channelType,
		ClientMsgNo: cmd.ClientMsgNo, Payload: cmd.Payload, NoPersist: cmd.NoPersist, SyncOnce: cmd.SyncOnce}, nil
}
