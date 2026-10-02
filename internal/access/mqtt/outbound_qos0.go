package mqtt

import (
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
)

// SendQoS0 enqueues a trusted prepared original under exact-owner execution.
// The caller serializes preparation/enqueue/completion, proves current receive
// permission and calls CompleteQoS0 only after success. Entry owns no durable
// completion or retry. QoS 0 shares the send gate but consumes no QoS 1 credit.
func (h *Handler) SendQoS0(g gt.Context, d sessioncase.PreparedDelivery) (err error) {
	s := h.state(g)
	defer func() {
		if recover() != nil {
			h.terminate(g, s, 0)
			err = ErrHandlerCallback
		}
	}()
	if s == nil || g.RequestContext == nil || d.Owner != s.connection.Owner || d.QoS != 0 || d.Exchange != (meta.MQTTInflight{}) {
		return ErrOutboundInvalid
	}
	s.mu.Lock()
	if !s.opened || s.closing {
		s.mu.Unlock()
		return ErrHandlerClosed
	}
	if s.sending {
		s.mu.Unlock()
		return ErrOutboundBusy
	}
	s.sending = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.sending = false; s.mu.Unlock() }()
	op, err := h.options.Owners.Begin(g.RequestContext, d.Owner)
	if err != nil {
		return outboundOwnerError(err)
	}
	defer op.Done()
	if op.UID() != s.connection.UID {
		return ErrOutboundInvalid
	}
	p, err := mapOutboundPublication(d.Publication, d.Topic, d.SubscriptionIdentifier, s.connection.UID, 0, h.options.Now().UnixMilli())
	if err != nil {
		return err
	}
	if op.Check() != nil || g.RequestContext.Err() != nil {
		return ErrHandlerClosed
	}
	s.mu.Lock()
	closed := s.closing
	s.mu.Unlock()
	if closed {
		return ErrHandlerClosed
	}
	// The common gateway writer serializes encoding, enqueue and physical close.
	if op.Check() != nil || g.RequestContext.Err() != nil || g.WritePacket(p) != nil {
		return h.terminate(g, s, 0)
	}
	if op.Check() != nil || g.RequestContext.Err() != nil {
		return h.terminate(g, s, 0)
	}
	return nil
}
