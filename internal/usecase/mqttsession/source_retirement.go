package mqttsession

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// SourceRetirementMetadata reads Session proof and retires source tombstones.
type SourceRetirementMetadata interface {
	ReadMQTT(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
	RetireMQTTSourceBinding(context.Context, meta.MQTTSourceBindingKey, uint64, uint64) (meta.MQTTSourceBindingResult, error)
}

type SourceRetirementOptions struct {
	Store   SourceRetirementMetadata
	Timeout time.Duration
}

// SourceRetirement deletes one acknowledged Removed binding once its Session
// lifetime is proven irreversible. The source Slot keeps a closed-lifetime
// fence and replay marker, so retirement never reopens admission or hides
// replay cleanup.
type SourceRetirement struct{ options SourceRetirementOptions }

func NewSourceRetirement(o SourceRetirementOptions) (*SourceRetirement, error) {
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Store == nil || o.Timeout <= 0 || o.Timeout > time.Minute {
		return nil, ErrInvalid
	}
	return &SourceRetirement{options: o}, nil
}

// Reconcile performs at most one retirement CAS. It returns false without
// writing while the lifetime is live or the Session row is absent: absence is
// not proof that no newer lifetime could reuse the generation.
func (p *SourceRetirement) Reconcile(parent context.Context, key meta.MQTTSourceBindingKey) (bool, error) {
	q := meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: key}
	if p == nil || parent == nil || meta.ValidateMQTTRead(q) != nil {
		return false, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, p.options.Timeout)
	defer cancel()
	r, err := p.read(ctx, q)
	if err != nil {
		return false, err
	}
	if r.Session != nil || len(r.Subscriptions) != 0 || len(r.Bindings) > 1 {
		return false, ErrEvidence
	}
	if len(r.Bindings) == 0 {
		return false, nil
	}
	b := r.Bindings[0]
	if b.Key != key || meta.ValidateMQTTSourceBinding(b) != nil {
		return false, ErrEvidence
	}
	if b.Stage != meta.MQTTBindingRemoved || key.Owner.Kind == meta.MQTTBindingChannel && b.ProtectionRevision == 0 {
		return false, nil
	}
	r, err = p.read(ctx, meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: key.Namespace, ClientID: key.ClientID})
	if err != nil {
		return false, err
	}
	if len(r.Bindings) != 0 || len(r.Subscriptions) != 0 {
		return false, ErrEvidence
	}
	s := r.Session
	if s == nil {
		return false, nil
	}
	if !removalSessionMatches(s, b) {
		return false, ErrEvidence
	}
	var closed uint64
	switch {
	case s.Generation > key.SessionGeneration:
		// Every earlier lifetime is superseded and can never be resumed.
		closed = s.Generation - 1
	case s.State == meta.MQTTSessionEnded:
		closed = s.Generation
	default:
		return false, nil
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	res, err := p.options.Store.RetireMQTTSourceBinding(ctx, key, b.Revision, closed)
	if err != nil {
		return false, err
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	switch res.Status {
	case meta.MQTTSessionCASApplied:
		return true, nil
	case meta.MQTTSessionCASConflict:
		return false, ErrConflict
	}
	return false, ErrEvidence
}

func (p *SourceRetirement) read(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	if err := ctx.Err(); err != nil {
		return meta.MQTTReadResult{}, err
	}
	r, err := p.options.Store.ReadMQTT(ctx, q)
	if err != nil {
		return meta.MQTTReadResult{}, err
	}
	if err = ctx.Err(); err != nil {
		return meta.MQTTReadResult{}, err
	}
	if !r.Done || r.After != (meta.MQTTReadCursor{}) || r.Runtime != nil || r.Admission != nil || r.Membership != nil || r.Accounting != nil || len(r.Directory) != 0 || len(r.SourceOwners) != 0 || len(r.Sessions) != 0 || len(r.Inflight) != 0 || len(r.Wills) != 0 || len(r.DeliveryCursors) != 0 {
		return meta.MQTTReadResult{}, ErrEvidence
	}
	return r, nil
}
