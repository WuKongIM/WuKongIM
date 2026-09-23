package mqttsession

import (
	"context"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// SourceProgressMetadata reads through current Slot authority. Exact cursor
// reads must pin the current Session and the requested cursor in one snapshot.
type SourceProgressMetadata interface {
	ReadMQTT(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
	CompareAndSwapMQTTSourceBinding(context.Context, uint64, meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error)
}

type SourceProgressOptions struct {
	Store SourceProgressMetadata
	// Now timestamps projection writes; Timeout bounds the whole reconciliation.
	Now     func() time.Time
	Timeout time.Duration
}

// SourceProgress projects contiguous consumer completion into a source-owned
// obligation. It owns no worker, per-consumer cache, deletion or release port.
type SourceProgress struct{ options SourceProgressOptions }

type SourceProgressResult struct {
	Binding meta.MQTTSourceBinding
	Changed bool
	// NeedsRemoval preserves the separate source-side release/acknowledgement
	// obligation; it never grants permission to delete shared content.
	NeedsRemoval bool
}

func NewSourceProgress(o SourceProgressOptions) (*SourceProgress, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Store == nil || o.Timeout <= 0 || o.Timeout > time.Minute {
		return nil, ErrInvalid
	}
	return &SourceProgress{options: o}, nil
}

// Reconcile performs at most two point reads and one CAS. Durable completion
// and explicit lifetime termination are monotonic across connection takeovers;
// they need no live owner lease and confer no delivery or subscription authority.
func (p *SourceProgress) Reconcile(parent context.Context, key meta.MQTTSourceBindingKey) (SourceProgressResult, error) {
	var out SourceProgressResult
	q := meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: key}
	if p == nil || parent == nil || key.Owner.Kind != meta.MQTTBindingChannel || meta.ValidateMQTTRead(q) != nil {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, p.options.Timeout)
	defer cancel()
	r, err := p.read(ctx, q)
	if err != nil {
		return out, err
	}
	if r.Session != nil || len(r.DeliveryCursors) != 0 || len(r.Bindings) != 1 {
		return out, ErrEvidence
	}
	b := r.Bindings[0]
	if b.Key != key || meta.ValidateMQTTSourceBinding(b) != nil {
		return out, ErrEvidence
	}
	out = SourceProgressResult{Binding: b, NeedsRemoval: b.Stage == meta.MQTTBindingRemoving}
	if b.Stage == meta.MQTTBindingRemoved {
		return out, nil
	}
	cursorKey := meta.MQTTDeliveryCursorKey{Namespace: key.Namespace, ClientID: key.ClientID, SessionGeneration: key.SessionGeneration, SubscriptionGeneration: key.SubscriptionGeneration, SourceKind: meta.MQTTSourceChannel, SourceID: key.Owner.ID, SourceGeneration: key.Owner.Generation}
	r, err = p.read(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: cursorKey})
	if err != nil {
		return SourceProgressResult{}, err
	}
	if r.Session == nil || len(r.Bindings) != 0 || len(r.DeliveryCursors) > 1 {
		return SourceProgressResult{}, ErrEvidence
	}
	s := r.Session
	if meta.ValidateMQTTSession(*s) != nil || s.Namespace != key.Namespace || s.ClientID != key.ClientID || s.UID != b.UID || s.Generation < key.SessionGeneration || s.Revision < b.ProgressRevision {
		return SourceProgressResult{}, ErrEvidence
	}
	var cursor meta.MQTTDeliveryCursor
	if len(r.DeliveryCursors) == 1 {
		cursor = r.DeliveryCursors[0]
		if meta.ValidateMQTTDeliveryCursor(cursor) != nil || cursor.Key != cursorKey || cursor.Topic != b.Topic || cursor.AuthorizationVersion != b.AuthorizationVersion || cursor.Revision > s.Revision || (b.BoundaryKnown && cursor.StartAfter != b.StartAfter) {
			return SourceProgressResult{}, ErrEvidence
		}
	}
	next := b
	if s.Generation > key.SessionGeneration || s.State == meta.MQTTSessionEnded {
		// Termination is a Session decision, not an inferred lease/expiry event.
		// Old cursors may be absent or older than an already projected end proof.
		if b.ReleaseReason == meta.MQTTBindingSessionEnded {
			return out, nil
		}
		if s.Revision <= b.ProgressRevision {
			return SourceProgressResult{}, ErrEvidence
		}
		next.Stage, next.ReleaseReason = meta.MQTTBindingRemoving, meta.MQTTBindingSessionEnded
		next.ProgressRevision = s.Revision
	} else {
		if b.ReleaseReason == meta.MQTTBindingSessionEnded {
			return SourceProgressResult{}, ErrEvidence
		}
		if !b.BoundaryKnown && len(r.DeliveryCursors) == 0 {
			return out, nil
		}
		if !b.BoundaryKnown || len(r.DeliveryCursors) != 1 || cursor.Revision < b.ProgressRevision || cursor.CompletedThrough < b.CompletedThrough {
			return SourceProgressResult{}, ErrEvidence
		}
		completed := cursor.CompletedThrough
		if b.EndKnown {
			completed = min(completed, b.EndThrough)
		}
		if completed == b.CompletedThrough {
			return out, nil
		}
		if cursor.Revision <= b.ProgressRevision {
			return SourceProgressResult{}, ErrEvidence
		}
		next.CompletedThrough, next.ProgressRevision = completed, cursor.Revision
	}
	return p.write(ctx, b, next)
}

// read rejects partial pages and unrelated collections before interpreting a
// point result. Absence is never treated as evidence that a consumer completed.
func (p *SourceProgress) read(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
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
	if !r.Done || r.After != (meta.MQTTReadCursor{}) || len(r.SourceOwners) != 0 || len(r.Sessions) != 0 || len(r.Subscriptions) != 0 || len(r.Inflight) != 0 || len(r.Wills) != 0 {
		return meta.MQTTReadResult{}, ErrEvidence
	}
	return r, nil
}

func (p *SourceProgress) write(ctx context.Context, old, next meta.MQTTSourceBinding) (SourceProgressResult, error) {
	var out SourceProgressResult
	now := p.options.Now().UnixMilli()
	if old.Revision == math.MaxUint64 || now <= 0 || now < old.UpdatedAtMS {
		return out, ErrEvidence
	}
	next.Revision, next.UpdatedAtMS, next.RecoveryAtMS = old.Revision+1, now, now
	if meta.ValidateMQTTSourceBinding(next) != nil {
		return out, ErrEvidence
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	r, err := p.options.Store.CompareAndSwapMQTTSourceBinding(ctx, old.Revision, next)
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if r.Status == meta.MQTTSessionCASConflict {
		return out, ErrConflict
	}
	if (r.Status != meta.MQTTSessionCASApplied && r.Status != meta.MQTTSessionCASUnchanged) || r.CurrentRevision != next.Revision {
		return out, ErrEvidence
	}
	return SourceProgressResult{Binding: next, Changed: r.Status == meta.MQTTSessionCASApplied, NeedsRemoval: next.Stage == meta.MQTTBindingRemoving}, nil
}
