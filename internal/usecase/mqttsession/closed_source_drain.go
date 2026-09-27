package mqttsession

import (
	"context"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func (p *SourceDrain) checkDrainSession(ctx context.Context, op *closedIntentScope, o contract.Owner, row *meta.MQTTSession) error {
	return op.checkSession(ctx, p.guard, o, row)
}

// ReconcileClosed rereads one exact binding and parent before bounded cleanup.
// It never follows another lifetime/Owner once captured, never activates a
// Session, and requires closed intent again inside every shared sealing turn.
func (p *SourceDrain) ReconcileClosed(parent context.Context, key meta.MQTTSourceBindingKey) (SourceDrainResult, error) {
	return p.reconcileClosed(parent, key, nil)
}

// SealClosed inherits the enclosing cleanup's captured Owner. A nested turn
// cannot obtain fresh authority from a successor between the caller's checks.
func (p *SourceDrain) SealClosed(parent context.Context, owner contract.Owner, key meta.MQTTSourceBindingKey) (SourceDrainResult, error) {
	if owner == (contract.Owner{}) {
		return SourceDrainResult{}, ErrInvalid
	}
	return p.reconcileClosed(parent, key, &owner)
}

func (p *SourceDrain) reconcileClosed(parent context.Context, key meta.MQTTSourceBindingKey, expected *contract.Owner) (out SourceDrainResult, err error) {
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
	op := &closedIntentScope{}
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
	if expected != nil && o != *expected {
		return out, ErrFenced
	}
	if err = p.checkDrainSession(ctx, op, o, r.Session); err != nil {
		return out, err
	}
	return p.seal(ctx, op, o, key)
}
