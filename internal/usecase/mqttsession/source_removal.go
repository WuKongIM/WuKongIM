package mqttsession

import (
	"context"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type SourceRemovalOptions struct {
	Store SourceProgressMetadata
	// Now timestamps source acknowledgements; Timeout bounds the complete turn.
	Now     func() time.Time
	Timeout time.Duration
}

// SourceRemoval discharges one consumer obligation through source-owned Slot
// commits. It never releases aggregate Channel protection or shared content.
type SourceRemoval struct{ options SourceRemovalOptions }

type SourceRemovalResult struct {
	Binding meta.MQTTSourceBinding
	Changed bool
}

func NewSourceRemoval(o SourceRemovalOptions) (*SourceRemoval, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Store == nil || o.Timeout <= 0 || o.Timeout > time.Minute {
		return nil, ErrInvalid
	}
	return &SourceRemoval{options: o}, nil
}

// Reconcile acknowledges a proven removal before a separate turn removes its
// consumer indexes. Each turn rereads remote proof and performs at most one CAS;
// intervening binding changes require a new acknowledgement of that revision.
func (p *SourceRemoval) Reconcile(parent context.Context, key meta.MQTTSourceBindingKey) (SourceRemovalResult, error) {
	var out SourceRemovalResult
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
	if r.Session != nil || len(r.Subscriptions) != 0 || len(r.DeliveryCursors) != 0 || len(r.Bindings) != 1 {
		return out, ErrEvidence
	}
	b := r.Bindings[0]
	if b.Key != key || meta.ValidateMQTTSourceBinding(b) != nil {
		return out, ErrEvidence
	}
	out.Binding = b
	if b.Stage != meta.MQTTBindingRemoving {
		return out, nil
	}
	if b.ReleaseReason != meta.MQTTBindingSessionEnded {
		drained, err := p.drained(ctx, b)
		if err != nil {
			return SourceRemovalResult{}, err
		}
		if !drained {
			return out, nil
		}
		return p.write(ctx, b)
	}
	r, err = p.read(ctx, meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: key.Namespace, ClientID: key.ClientID})
	if err != nil {
		return SourceRemovalResult{}, err
	}
	if len(r.Bindings) != 0 || len(r.Subscriptions) != 0 || len(r.DeliveryCursors) != 0 || !removalSessionMatches(r.Session, b) ||
		(r.Session.Generation == key.SessionGeneration && r.Session.State != meta.MQTTSessionEnded) {
		return SourceRemovalResult{}, ErrEvidence
	}
	return p.write(ctx, b)
}

// drained first proves admission closed, then reads completion. Subscription
// generations and Removing are monotonic, so later ACKs cannot reopen admission.
func (p *SourceRemoval) drained(ctx context.Context, b meta.MQTTSourceBinding) (bool, error) {
	if !b.BoundaryKnown || !b.EndKnown || b.ProgressRevision == 0 || b.CompletedThrough != b.EndThrough {
		return false, nil
	}
	k := b.Key
	r, err := p.read(ctx, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: k.Namespace, ClientID: k.ClientID, SessionGeneration: k.SessionGeneration, Topic: b.Topic})
	if err != nil {
		return false, err
	}
	if len(r.Bindings) != 0 || len(r.DeliveryCursors) != 0 || len(r.Subscriptions) != 1 || !removalSessionMatches(r.Session, b) {
		return false, ErrEvidence
	}
	if r.Session.Generation != k.SessionGeneration || r.Session.State == meta.MQTTSessionEnded {
		// SourceProgress must first project explicit lifetime termination.
		return false, nil
	}
	sub := r.Subscriptions[0]
	if meta.ValidateMQTTSubscription(sub) != nil || sub.Namespace != k.Namespace || sub.ClientID != k.ClientID || sub.SessionGeneration != k.SessionGeneration ||
		sub.Topic != b.Topic || sub.Generation < k.SubscriptionGeneration || sub.Revision < b.IntentRevision || sub.Revision > r.Session.Revision {
		return false, ErrEvidence
	}
	if sub.Generation == k.SubscriptionGeneration {
		if sub.OperationID != b.OperationID || sub.AuthorizationVersion != b.AuthorizationVersion ||
			(sub.TargetKind == meta.MQTTSubscriptionGroup && k.Owner.ID != "2:"+sub.TargetID) ||
			(sub.TargetKind == meta.MQTTSubscriptionUserInbox && sub.TargetID != b.UID) {
			return false, ErrEvidence
		}
		if sub.Stage < meta.MQTTSubscriptionRemoving {
			return false, nil
		}
	} else if sub.Revision <= b.IntentRevision || sub.OperationID == b.OperationID {
		return false, ErrEvidence
	}
	sessionRevision := r.Session.Revision
	cursorKey := meta.MQTTDeliveryCursorKey{Namespace: k.Namespace, ClientID: k.ClientID, SessionGeneration: k.SessionGeneration, SubscriptionGeneration: k.SubscriptionGeneration, SourceKind: meta.MQTTSourceChannel, SourceID: k.Owner.ID, SourceGeneration: k.Owner.Generation}
	r, err = p.read(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: cursorKey})
	if err != nil {
		return false, err
	}
	if len(r.Bindings) != 0 || len(r.Subscriptions) != 0 || len(r.DeliveryCursors) != 1 || !removalSessionMatches(r.Session, b) || r.Session.Revision < sessionRevision {
		return false, ErrEvidence
	}
	if r.Session.Generation != k.SessionGeneration || r.Session.State == meta.MQTTSessionEnded {
		return false, nil
	}
	c := r.DeliveryCursors[0]
	if meta.ValidateMQTTDeliveryCursor(c) != nil || c.Key != cursorKey || c.Topic != b.Topic || c.AuthorizationVersion != b.AuthorizationVersion ||
		c.StartAfter != b.StartAfter || c.Revision < b.ProgressRevision || c.Revision > r.Session.Revision || c.AccountedThrough != b.EndThrough || c.CompletedThrough < b.CompletedThrough {
		return false, ErrEvidence
	}
	return c.CompletedThrough == b.EndThrough && c.PendingMessages == 0 && c.PendingBytes == 0 && c.InflightCount == 0 && c.InflightBytes == 0, nil
}

func removalSessionMatches(s *meta.MQTTSession, b meta.MQTTSourceBinding) bool {
	return s != nil && meta.ValidateMQTTSession(*s) == nil && s.Namespace == b.Key.Namespace && s.ClientID == b.Key.ClientID && s.UID == b.UID &&
		s.Generation >= b.Key.SessionGeneration && s.Revision >= b.ProgressRevision && s.Revision >= b.IntentRevision
}

// read checks cancellation and the closed point-result envelope. The caller
// validates the selected collection, its exact identity and proof revisions.
func (p *SourceRemoval) read(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
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
	if !r.Done || r.After != (meta.MQTTReadCursor{}) || len(r.SourceOwners) != 0 || len(r.Sessions) != 0 || len(r.Inflight) != 0 || len(r.Wills) != 0 {
		return meta.MQTTReadResult{}, ErrEvidence
	}
	return r, nil
}

func (p *SourceRemoval) write(ctx context.Context, old meta.MQTTSourceBinding) (SourceRemovalResult, error) {
	var out SourceRemovalResult
	now := p.options.Now().UnixMilli()
	if old.Revision == math.MaxUint64 || now <= 0 || now < old.UpdatedAtMS {
		return out, ErrEvidence
	}
	next := old
	next.Revision, next.UpdatedAtMS, next.RecoveryAtMS = old.Revision+1, now, now
	if old.ProtectionRevision == old.Revision {
		next.Stage, next.RecoveryAtMS = meta.MQTTBindingRemoved, 0
		if next.ReleaseReason == 0 {
			next.ReleaseReason = meta.MQTTBindingDrained
		}
	} else {
		// This source-Slot commit acknowledges only this exact consumer removal.
		// Retaining Removing keeps both recovery and retention conservative.
		next.ProtectionRevision = next.Revision
	}
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
	return SourceRemovalResult{Binding: next, Changed: r.Status == meta.MQTTSessionCASApplied}, nil
}
