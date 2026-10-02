package mqtt

import (
	"context"
	"errors"
	"strings"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
)

// SessionSubscriptions returns only confirmed intent transitions. Implementations
// own pending convergence and mark errors that may retain a changed intent with
// ErrSubscriptionUnconfirmed; entry never repeats uncertain business effects.
type SessionSubscriptions interface {
	Subscribe(context.Context, contract.Owner, sessioncase.SubscriptionRequest) (meta.MQTTSubscription, error)
	Unsubscribe(context.Context, contract.Owner, string) (bool, error)
}

type subscriptionInput struct {
	request sessioncase.SubscriptionRequest
	reason  byte
}

// subscriptionPacket maps one bounded, ordered batch and retains owner execution
// through reply enqueue. A later unknown result cannot emit a partial batch ACK.
func (h *Handler) subscriptionPacket(g gt.Context, s *connectionState, packet any) error {
	operation := "subscribe"
	if _, ok := packet.(*wire.Unsubscribe); ok {
		operation = "unsubscribe"
	}
	diagnostic := ""
	defer func() {
		if p := recover(); p != nil {
			if diagnostic == "" {
				diagnostic = "callback"
			}
			h.observeSubscriptionClose(operation, diagnostic)
			panic(p) // OnPacket owns callback failure and transport cleanup.
		}
		if diagnostic != "" {
			h.observeSubscriptionClose(operation, diagnostic)
		}
	}()
	closeWith := func(reason string, wireReason byte) error {
		diagnostic = reason
		return h.terminate(g, s, wireReason)
	}
	if h.options.Subscriptions == nil {
		return closeWith("disabled", 0x83)
	}
	id, inputs, subscribe, err := mapSubscriptionPacket(packet)
	if err != nil {
		return closeWith("malformed", wire.ProtocolError)
	}
	ctx, cancel := context.WithTimeout(g.RequestContext, h.options.SubscriptionTimeout)
	defer cancel()
	op, err := h.options.Owners.Begin(ctx, s.connection.Owner)
	if err != nil {
		return closeWith(subscriptionCloseReason(err), 0)
	}
	defer op.Done()
	deadline, _ := ctx.Deadline()
	callCtx, cancelCall := context.WithDeadline(op.Context(), deadline)
	defer cancelCall()
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return op.Check()
	}
	reasons := make([]byte, len(inputs))
	for i, input := range inputs {
		if err := check(); err != nil {
			return closeWith(subscriptionCloseReason(err), 0)
		}
		if input.reason != 0 {
			reasons[i] = input.reason
			continue
		}
		if subscribe {
			row, e := h.options.Subscriptions.Subscribe(callCtx, s.connection.Owner, input.request)
			if err := check(); err != nil {
				return closeWith(subscriptionCloseReason(err), 0)
			}
			if e != nil {
				reason := subscriptionFailureReason(e)
				if reason == 0 {
					return closeWith(subscriptionCloseReason(e), 0x83)
				}
				reasons[i] = reason
			} else {
				if !validSubscriptionReply(row, s.connection.Owner, input.request) {
					return closeWith("reply_evidence", 0x83)
				}
				reasons[i] = row.GrantedQoS
			}
		} else {
			existed, e := h.options.Subscriptions.Unsubscribe(callCtx, s.connection.Owner, input.request.Topic)
			if err := check(); err != nil {
				return closeWith(subscriptionCloseReason(err), 0)
			}
			if e != nil {
				return closeWith(subscriptionCloseReason(e), 0)
			}
			if !existed {
				reasons[i] = 0x11
			}
		}
	}
	var reply any = &wire.Unsuback{PacketID: id, Reasons: reasons}
	if subscribe {
		reply = &wire.Suback{PacketID: id, Reasons: reasons}
	}
	if err := check(); err != nil {
		return closeWith(subscriptionCloseReason(err), 0)
	}
	if g.WritePacket(reply) != nil {
		return closeWith("reply_write", 0)
	}
	if err := check(); err != nil {
		return closeWith(subscriptionCloseReason(err), 0)
	}
	op.Done()
	h.wakeDelivery(s.connection.Owner)
	return nil
}

// observeSubscriptionClose cannot replace the original result or interrupt cleanup.
func (h *Handler) observeSubscriptionClose(operation, reason string) {
	if h.options.ObserveSubscriptionClose == nil {
		return
	}
	defer func() { _ = recover() }()
	h.options.ObserveSubscriptionClose(operation, reason)
}

// subscriptionCloseReason diagnoses the underlying failure even when joined
// with unconfirmed intent. It grants no retry, ACK or isolation authority.
func subscriptionCloseReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, runtime.ErrOwnerLimit):
		return "owner_limit"
	case errors.Is(err, runtime.ErrOwnerFenced), errors.Is(err, runtime.ErrOwnerStopped), errors.Is(err, sessioncase.ErrFenced):
		return "fenced"
	case errors.Is(err, sessioncase.ErrClock):
		return "clock"
	case errors.Is(err, sessioncase.ErrConflict):
		return "conflict"
	case errors.Is(err, sessioncase.ErrEvidence):
		return "evidence"
	case errors.Is(err, sessioncase.ErrReplayPending), errors.Is(err, sessioncase.ErrSourceDrainPending):
		return "pending"
	case errors.Is(err, sessioncase.ErrSubscriptionCallback):
		return "callback"
	case errors.Is(err, sessioncase.ErrSubscriptionDenied), errors.Is(err, sessioncase.ErrSubscriptionRevoked):
		return "denied"
	case errors.Is(err, sessioncase.ErrSubscriptionLimit):
		return "quota"
	case errors.Is(err, sessioncase.ErrSubscriptionUnconfirmed):
		return "unconfirmed"
	default:
		return "unknown"
	}
}

// mapSubscriptionPacket validates the entire packet envelope before any effect;
// syntactically valid unsupported filters retain their per-filter reply position.
func mapSubscriptionPacket(packet any) (uint16, []subscriptionInput, bool, error) {
	var id uint16
	var properties []wire.Property
	var filters []wire.Subscription
	subscribe := false
	switch p := packet.(type) {
	case *wire.Subscribe:
		if p == nil {
			return 0, nil, false, ErrHandlerInvalid
		}
		id, properties, filters, subscribe = p.PacketID, p.Properties, p.Subscriptions, true
	case *wire.Unsubscribe:
		if p == nil || len(p.Filters) == 0 || len(p.Filters) > 128 {
			return 0, nil, false, ErrHandlerInvalid
		}
		id, properties = p.PacketID, p.Properties
		filters = make([]wire.Subscription, len(p.Filters))
		for i, topic := range p.Filters {
			filters[i].Filter = topic
		}
	default:
		return 0, nil, false, ErrHandlerInvalid
	}
	if id == 0 || len(filters) == 0 || len(filters) > 128 || len(properties) > 128 {
		return 0, nil, false, ErrHandlerInvalid
	}
	identifier := uint32(0)
	for _, p := range properties {
		switch p.ID {
		case wire.SubscriptionIdentifier:
			if !subscribe || identifier != 0 || p.Number == 0 || p.Number > 268435455 {
				return 0, nil, false, ErrHandlerInvalid
			}
			identifier = p.Number
		case wire.UserProperty:
			if strings.HasPrefix(p.Text, "wk.") {
				return 0, nil, false, ErrHandlerInvalid
			}
		default:
			return 0, nil, false, ErrHandlerInvalid
		}
	}
	inputs := make([]subscriptionInput, len(filters))
	for i, f := range filters {
		if f.QoS > 2 || f.RetainHandling > 2 {
			return 0, nil, false, ErrHandlerInvalid
		}
		r := sessioncase.SubscriptionRequest{Topic: f.Filter, RequestedQoS: f.QoS, NoLocal: f.NoLocal, RetainAsPublished: f.RetainAsPublished, RetainHandling: f.RetainHandling, SubscriptionIdentifier: identifier}
		reason := byte(0)
		target, err := ParseTopic(f.Filter)
		switch {
		case strings.HasPrefix(f.Filter, "$share/") && subscribe:
			reason = 0x9e
		case strings.ContainsAny(f.Filter, "+#") && subscribe:
			reason = 0xa2
		case err != nil:
			reason = 0x8f
		default:
			r.TargetID = target.ChannelID
			r.TargetKind = meta.MQTTSubscriptionGroup
			if target.ChannelType == 1 {
				r.TargetKind = meta.MQTTSubscriptionUserInbox
			}
		}
		inputs[i] = subscriptionInput{request: r, reason: reason}
	}
	return id, inputs, subscribe, nil
}

func subscriptionFailureReason(err error) byte {
	if errors.Is(err, sessioncase.ErrSubscriptionUnconfirmed) {
		return 0
	}
	switch {
	case errors.Is(err, sessioncase.ErrSubscriptionDenied), errors.Is(err, sessioncase.ErrSubscriptionRevoked):
		return 0x87
	case errors.Is(err, sessioncase.ErrSubscriptionLimit):
		return 0x97
	default:
		return 0
	}
}

func validSubscriptionReply(row meta.MQTTSubscription, o contract.Owner, r sessioncase.SubscriptionRequest) bool {
	return meta.ValidateMQTTSubscription(row) == nil && row.Namespace == o.Key.Namespace && row.ClientID == o.Key.ClientID && row.SessionGeneration == o.SessionGeneration && row.Topic == r.Topic && row.Stage == meta.MQTTSubscriptionActive && row.TargetKind == r.TargetKind && row.TargetID == r.TargetID && row.GrantedQoS == min(r.RequestedQoS, 1) && row.NoLocal == r.NoLocal && row.RetainAsPublished == r.RetainAsPublished && row.RetainHandling == r.RetainHandling && row.SubscriptionIdentifier == r.SubscriptionIdentifier
}
