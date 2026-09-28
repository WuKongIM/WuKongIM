package mqttsession

import (
	"context"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// SourceRetirementMetadata reads Session proof and retires source tombstones.
type SourceRetirementMetadata interface {
	ReadMQTT(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
	// RetireLiveMQTTSourceBinding fences ended subscriptions of a live Session.
	RetireLiveMQTTSourceBinding(context.Context, meta.MQTTSourceBindingKey, uint64, uint64) (meta.MQTTSourceBindingResult, error)
	RetireMQTTSourceBinding(context.Context, meta.MQTTSourceBindingKey, uint64, uint64) (meta.MQTTSourceBindingResult, error)
	// CompareAndSwapMQTTSourceBinding defers an unprovable scheduled tombstone.
	CompareAndSwapMQTTSourceBinding(context.Context, uint64, meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error)
}

type SourceRetirementOptions struct {
	Store SourceRetirementMetadata
	// Now timestamps deferrals; it must not run behind stored UpdatedAtMS.
	Now     func() time.Time
	Timeout time.Duration
}

// Deferral bounds: an unprovable tombstone waits at least the minimum and its
// delay doubles up to the cap, so unretirable rows cost O(1) scans per cap.
const (
	minRetirementDeferral = time.Second
	maxRetirementDeferral = 10 * time.Minute
)

// SourceRetirement deletes one acknowledged Removed binding once its Session
// lifetime is proven irreversible. The source Slot keeps a closed-lifetime
// fence and replay marker, so retirement never reopens admission or hides
// replay cleanup.
type SourceRetirement struct{ options SourceRetirementOptions }

func NewSourceRetirement(o SourceRetirementOptions) (*SourceRetirement, error) {
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
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
	if b.Stage != meta.MQTTBindingRemoved {
		return false, nil
	}
	if key.Owner.Kind == meta.MQTTBindingChannel && b.ProtectionRevision == 0 {
		return false, p.postpone(ctx, b)
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
		return false, p.postpone(ctx, b)
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
	case s.State != meta.MQTTSessionEnded:
		// Live Session: new subscriptions take Revision+1, so a fence through
		// this generation is safe once every lower-or-equal one has ended.
		ok, e := p.endedThrough(ctx, s, key.SubscriptionGeneration)
		if e != nil {
			return false, e
		}
		if !ok {
			return false, p.postpone(ctx, b)
		}
	default:
		return false, p.postpone(ctx, b)
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	var res meta.MQTTSourceBindingResult
	if closed != 0 {
		res, err = p.options.Store.RetireMQTTSourceBinding(ctx, key, b.Revision, closed)
	} else {
		res, err = p.options.Store.RetireLiveMQTTSourceBinding(ctx, key, b.Revision, key.SubscriptionGeneration)
	}
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

// maxLiveRetirementPages bounds one live proof to 16*64 subscriptions.
const maxLiveRetirementPages = 16

// endedThrough proves every subscription of the live Session with generation
// <= through is Removed. Unfinished or oversized scans return false.
func (p *SourceRetirement) endedThrough(ctx context.Context, s *meta.MQTTSession, through uint64) (bool, error) {
	if through > s.Revision {
		return false, ErrEvidence
	}
	q := meta.MQTTRead{Kind: meta.MQTTReadSubscriptions, Namespace: s.Namespace, ClientID: s.ClientID, SessionGeneration: s.Generation, Limit: 64}
	for page := 0; page < maxLiveRetirementPages; page++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		r, err := p.options.Store.ReadMQTT(ctx, q)
		if err != nil {
			return false, err
		}
		// The page pins its Session row; it must be the proven lifetime unchanged.
		if r.Session != nil && (r.Session.Generation != s.Generation || r.Session.Revision != s.Revision || r.Session.State == meta.MQTTSessionEnded) {
			return false, nil
		}
		if len(r.Bindings) != 0 || len(r.Subscriptions) > q.Limit {
			return false, ErrEvidence
		}
		for _, sub := range r.Subscriptions {
			if sub.Namespace != s.Namespace || sub.ClientID != s.ClientID || sub.SessionGeneration != s.Generation {
				return false, ErrEvidence
			}
			if sub.Generation <= through && sub.Stage != meta.MQTTSubscriptionRemoved {
				return false, nil
			}
		}
		if r.Done {
			return true, nil
		}
		if r.After.Topic == "" || r.After.Topic <= q.After.Topic {
			return false, ErrEvidence
		}
		q.After.Topic = r.After.Topic
	}
	return false, nil
}

// postpone pushes one scheduled tombstone's next attempt back with a doubling
// delay. Unscheduled legacy rows (RecoveryAtMS 0) are not discoverable and stay
// untouched. A lost race is benign: the next discovery rereads the row.
func (p *SourceRetirement) postpone(ctx context.Context, b meta.MQTTSourceBinding) error {
	if b.RecoveryAtMS <= 0 {
		return nil
	}
	now := p.options.Now().UnixMilli()
	if now <= 0 || now < b.UpdatedAtMS || b.Revision == math.MaxUint64 {
		return ErrClock
	}
	delay := max(minRetirementDeferral.Milliseconds(), min(2*(b.RecoveryAtMS-b.UpdatedAtMS), maxRetirementDeferral.Milliseconds()))
	next := b
	next.Revision, next.UpdatedAtMS, next.RecoveryAtMS = b.Revision+1, now, max(now+delay, b.RecoveryAtMS+1)
	if meta.ValidateMQTTSourceBinding(next) != nil {
		return ErrEvidence
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	res, err := p.options.Store.CompareAndSwapMQTTSourceBinding(ctx, b.Revision, next)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	switch res.Status {
	case meta.MQTTSessionCASApplied, meta.MQTTSessionCASConflict:
		return nil
	}
	return ErrEvidence
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
