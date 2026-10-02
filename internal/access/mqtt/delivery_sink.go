package mqtt

import (
	"context"
	"errors"

	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
)

// BindDelivery returns this accepted connection and its entry-neutral sink.
// OnSessionOpen passes the pair to app after handshake release. App opens one
// Sender stream, retains it through cleanup and supplies bounded turn contexts.
func (h *Handler) BindDelivery(g gt.Context) (sessioncase.Connection, sessioncase.DeliverySink, error) {
	if h == nil || g.RequestContext == nil {
		return sessioncase.Connection{}, nil, ErrOutboundInvalid
	}
	if err := g.RequestContext.Err(); err != nil {
		return sessioncase.Connection{}, nil, err
	}
	s := h.state(g)
	if s == nil {
		return sessioncase.Connection{}, nil, ErrHandlerClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.opened || s.closing {
		return sessioncase.Connection{}, nil, ErrHandlerClosed
	}
	return s.connection, &gatewayDeliverySink{handler: h, gateway: g, state: s}, nil
}

// gatewayDeliverySink retains no publication body or exchange queue. Existing
// entry methods own packet mapping, credit, write serialization and ACK binding.
type gatewayDeliverySink struct {
	handler *Handler
	gateway gt.Context
	state   *connectionState
}

func (s *gatewayDeliverySink) Enqueue(ctx context.Context, d sessioncase.PreparedDelivery, dup bool) (sessioncase.DeliveryDisposition, error) {
	if ctx == nil || d.Owner != s.state.connection.Owner || s.handler.state(s.gateway) != s.state || d.QoS > 1 || d.QoS == 0 && dup {
		return 0, ErrOutboundInvalid
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	g := s.gateway
	g.RequestContext = ctx
	var err error
	if d.QoS == 1 {
		err = s.handler.SendQoS1(g, OutboundDelivery{Owner: d.Owner, Exchange: d.Exchange, Publication: d.Publication, Redelivery: dup})
	} else {
		err = s.handler.SendQoS0(g, d)
	}
	switch {
	case err == nil:
		return sessioncase.DeliveryQueued, nil
	case errors.Is(err, ErrOutboundBusy):
		return sessioncase.DeliveryBusy, nil
	case errors.Is(err, ErrOutboundExpired):
		return sessioncase.DeliveryExpired, nil
	default:
		return 0, err
	}
}

// Close emits bounded feedback and requests physical closure. Success means the
// close intent was dispatched; only Owners/End can establish quiescence proof.
func (s *gatewayDeliverySink) Close(ctx context.Context, reason meta.MQTTSessionEndReason) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrHandlerCallback
		}
	}()
	if ctx == nil || s.handler.state(s.gateway) != s.state {
		return ErrOutboundInvalid
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	var code byte
	switch reason {
	case 0:
	case meta.MQTTSessionRevoked:
		code = 0x87
	case meta.MQTTSessionQuota:
		code = 0x97
	case meta.MQTTSessionSourceLost, meta.MQTTSessionExplicit:
		code = 0x83
	default:
		return ErrOutboundInvalid
	}
	s.state.mu.Lock()
	if s.state.closing {
		code = 0
	}
	s.state.mu.Unlock()
	g := s.gateway
	g.RequestContext = ctx
	_ = s.handler.terminate(g, s.state, code)
	return nil
}
