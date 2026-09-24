package mqtt

import (
	"context"
	"errors"
	"strings"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
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
	if h.options.Subscriptions == nil {
		return h.terminate(g, s, 0x83)
	}
	id, inputs, subscribe, err := mapSubscriptionPacket(packet)
	if err != nil {
		return h.terminate(g, s, wire.ProtocolError)
	}
	ctx, cancel := context.WithTimeout(g.RequestContext, h.options.SubscriptionTimeout)
	defer cancel()
	op, err := h.options.Owners.Begin(ctx, s.connection.Owner)
	if err != nil {
		return h.terminate(g, s, 0)
	}
	defer op.Done()
	deadline, _ := ctx.Deadline()
	callCtx, cancelCall := context.WithDeadline(op.Context(), deadline)
	defer cancelCall()
	valid := func() bool { return ctx.Err() == nil && op.Check() == nil }
	reasons := make([]byte, len(inputs))
	for i, input := range inputs {
		if !valid() {
			return h.terminate(g, s, 0)
		}
		if input.reason != 0 {
			reasons[i] = input.reason
			continue
		}
		if subscribe {
			row, e := h.options.Subscriptions.Subscribe(callCtx, s.connection.Owner, input.request)
			if !valid() {
				return h.terminate(g, s, 0)
			}
			if e != nil {
				reason := subscriptionFailureReason(e)
				if reason == 0 {
					return h.terminate(g, s, 0x83)
				}
				reasons[i] = reason
			} else {
				if !validSubscriptionReply(row, s.connection.Owner, input.request) {
					return h.terminate(g, s, 0x83)
				}
				reasons[i] = row.GrantedQoS
			}
		} else {
			existed, e := h.options.Subscriptions.Unsubscribe(callCtx, s.connection.Owner, input.request.Topic)
			if !valid() || e != nil {
				return h.terminate(g, s, 0)
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
	if !valid() || g.WritePacket(reply) != nil || !valid() {
		return h.terminate(g, s, 0)
	}
	op.Done()
	h.wakeDelivery(s.connection.Owner)
	return nil
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
