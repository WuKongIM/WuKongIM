package mqttsession

import (
	"context"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// sourceDrainScope separates live execution from cleanup of durable closed
// intent. A background scope grants no Owner operation or network capability.
type sourceDrainScope struct {
	live *subscriptionOperation
	uid  string
}

func (s *sourceDrainScope) UID() string { return s.uid }
func (s *sourceDrainScope) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.live != nil {
		return s.live.Check()
	}
	return nil
}

func (p *SourceDrain) checkDrainSession(ctx context.Context, op *sourceDrainScope, o contract.Owner, row *meta.MQTTSession) error {
	if op.live != nil {
		return p.guard.checkSession(ctx, op.live, o, row)
	}
	if err := op.check(ctx); err != nil {
		return err
	}
	if row == nil {
		return ErrFenced
	}
	if meta.ValidateMQTTSession(*row) != nil {
		return ErrEvidence
	}
	if sessionOwner(*row) != o || row.UID != op.uid || row.State == meta.MQTTSessionEnded {
		return ErrFenced
	}
	now, err := p.guard.now()
	if err != nil {
		return err
	}
	if now.UnixMilli() < row.UpdatedAtMS {
		return ErrClock
	}
	return nil
}

// ReconcileClosed rereads one exact binding and parent before bounded cleanup.
// It never follows another lifetime/Owner once captured, never activates a
// Session, and requires closed intent again inside every shared sealing turn.
func (p *SourceDrain) ReconcileClosed(parent context.Context, key meta.MQTTSourceBindingKey) (out SourceDrainResult, err error) {
	q := meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: key}
	if p == nil || parent == nil || key.Owner.Kind != meta.MQTTBindingChannel || meta.ValidateMQTTRead(q) != nil {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, p.options.Timeout)
	defer cancel()
	defer func() {
		if recover() != nil {
			err = ErrSubscriptionCallback
		}
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			out = SourceDrainResult{}
		}
	}()
	op := &sourceDrainScope{}
	r, err := p.read(ctx, op, q)
	if err != nil {
		return out, err
	}
	if r.Session != nil || len(r.Subscriptions) != 0 || len(r.DeliveryCursors) != 0 || len(r.Bindings) != 1 {
		return out, ErrEvidence
	}
	b := r.Bindings[0]
	if meta.ValidateMQTTSourceBinding(b) != nil || b.Key != key {
		return out, ErrEvidence
	}
	op.uid = b.UID
	r, err = p.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: key.Namespace, ClientID: key.ClientID, SessionGeneration: key.SessionGeneration, Topic: b.Topic})
	if err != nil {
		return out, err
	}
	if r.Session == nil || len(r.Bindings) != 0 || len(r.DeliveryCursors) != 0 || len(r.Subscriptions) != 1 ||
		meta.ValidateMQTTSession(*r.Session) != nil || r.Session.Namespace != key.Namespace || r.Session.ClientID != key.ClientID || r.Session.Generation != key.SessionGeneration || r.Session.UID != b.UID {
		return out, ErrEvidence
	}
	o := sessionOwner(*r.Session)
	if err = p.checkDrainSession(ctx, op, o, r.Session); err != nil {
		return out, err
	}
	return p.seal(ctx, op, o, key)
}
