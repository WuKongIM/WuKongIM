package mqttsession

import (
	"context"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// InboxRemovalDrain seals one exact original incarnation and releases only its
// unadmitted backlog. Pending ranges and inflight exchanges remain durable.
type InboxRemovalDrain interface {
	Seal(context.Context, contract.Owner, meta.MQTTSourceBindingKey) (SourceDrainResult, error)
}

// InboxClosedRemovalDrain resumes only already closed durable source intent.
// It grants no live Owner operation or transport execution capability.
type InboxClosedRemovalDrain interface {
	SealClosed(context.Context, contract.Owner, meta.MQTTSourceBindingKey) (SourceDrainResult, error)
}

type InboxRemovalOptions struct {
	Store  InboxEstablishmentMetadata
	Owners *runtime.Owners
	Drain  InboxRemovalDrain
	// ClosedDrain permits background cleanup when no live Owner is admitted.
	ClosedDrain InboxClosedRemovalDrain
	// PageSize bounds one closed cursor page, default 8 and maximum 64.
	PageSize int
	// Timeout bounds the complete turn; Now shares owner monotonic time.
	Timeout time.Duration
	Now     func() time.Time
}

// InboxRemoval implements the Remove half of SubscriptionProjection. Its UID
// tombstone stops admission and records draining, never source/content release.
type InboxRemoval struct {
	options InboxRemovalOptions
	guard   *Subscriptions
}

func NewInboxRemoval(o InboxRemovalOptions) (*InboxRemoval, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.PageSize == 0 {
		o.PageSize = 8
	}
	if o.Store == nil || ((o.Owners == nil || o.Drain == nil) && o.ClosedDrain == nil) || o.PageSize < 1 || o.PageSize > 64 || o.Timeout <= 0 || o.Timeout > time.Minute {
		return nil, ErrInvalid
	}
	p := &InboxRemoval{options: o, guard: &Subscriptions{options: SubscriptionOptions{Owners: o.Owners, Now: o.Now, Timeout: o.Timeout}}}
	if _, err := p.guard.now(); err != nil {
		return nil, err
	}
	return p, nil
}

// Remove persists closed qualification before a bounded scan, retaining the
// current candidate while SourceDrain is pending. Receive permission is not a
// cleanup prerequisite; every effect still requires current owner/closed intent.
func (p *InboxRemoval) Remove(parent context.Context, r SubscriptionProjectionRequest) (out SubscriptionProjectionReceipt, err error) {
	if p == nil || p.options.Owners == nil || p.options.Drain == nil || meta.ValidateMQTTSubscription(r.Subscription) != nil || r.Subscription.TargetKind != meta.MQTTSubscriptionUserInbox {
		return out, ErrInvalid
	}
	if r.Subscription.Stage != meta.MQTTSubscriptionRemoving {
		return out, ErrConflict
	}
	op, ctx, cancel, err := p.guard.begin(parent, r.Owner)
	if err != nil {
		return out, err
	}
	defer func() {
		defer op.Done()
		defer cancel()
		if recover() != nil {
			err = ErrSubscriptionCallback
		}
		if stopped := checkSubscriptionScope(ctx, op); err == nil && stopped != nil {
			err = stopped
		}
		if err != nil {
			out = SubscriptionProjectionReceipt{}
		}
	}()
	return p.remove(ctx, &closedIntentScope{live: op, uid: op.UID()}, r)
}

// RemoveClosed drains one frozen Removing intent from authoritative metadata,
// including offline Sessions. It never follows a changed Owner or lifetime and
// returns no receipt after cancellation, late evidence or uncertain writes.
func (p *InboxRemoval) RemoveClosed(parent context.Context, r SubscriptionProjectionRequest) (out SubscriptionProjectionReceipt, err error) {
	if p == nil || parent == nil || p.options.ClosedDrain == nil || meta.ValidateMQTTSubscription(r.Subscription) != nil || r.Subscription.TargetKind != meta.MQTTSubscriptionUserInbox {
		return out, ErrInvalid
	}
	if r.Subscription.Stage != meta.MQTTSubscriptionRemoving {
		return out, ErrConflict
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
			out = SubscriptionProjectionReceipt{}
		}
	}()
	return p.remove(ctx, &closedIntentScope{uid: r.UID}, r)
}

func (p *InboxRemoval) remove(ctx context.Context, op *closedIntentScope, r SubscriptionProjectionRequest) (out SubscriptionProjectionReceipt, err error) {
	if r.UID != op.UID() || r.Subscription.TargetID != r.UID || r.Subscription.AuthorizationVersion != 0 {
		return out, ErrEvidence
	}
	t := inboxRemovalTurn{p: p, ctx: ctx, op: op, request: r}
	if _, err = t.current(); err != nil {
		return out, err
	}
	row, found, err := t.qualification()
	if err != nil {
		return out, err
	}
	if !found {
		// Generation is the original Preparing revision, preserving the later
		// closure witness even when preparation never wrote its qualification.
		if r.Subscription.Generation >= r.Subscription.Revision {
			return out, ErrEvidence
		}
		row = meta.MQTTSourceBinding{Key: t.key(), UID: r.UID, Topic: r.Subscription.Topic, OperationID: r.Subscription.OperationID, IntentRevision: r.Subscription.Generation, Stage: meta.MQTTBindingPreparing}
		row, err = t.write(row)
		if err != nil {
			return out, err
		}
	}
	if row.DrainVersion == 0 {
		row.Stage, row.IntentRevision, row.DrainVersion = meta.MQTTBindingRemoving, r.Subscription.Revision, 1
		row, err = t.write(row)
		if err != nil {
			return out, err
		}
	}
	if !row.DrainDone {
		q := meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: row.Key.Namespace, ClientID: row.Key.ClientID, SessionGeneration: row.Key.SessionGeneration, SubscriptionGeneration: row.Key.SubscriptionGeneration, Limit: p.options.PageSize, After: meta.MQTTReadCursor{Delivery: inboxDrainCursor(row)}}
		page, e := t.read(q)
		if e != nil {
			return out, e
		}
		if err = t.validatePage(row, q, page); err != nil {
			return out, err
		}
		for _, candidate := range page.DeliveryCursors {
			if _, err = t.current(); err != nil {
				return out, err
			}
			key := row.Key
			key.Owner = meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: candidate.Key.SourceID, Generation: candidate.Key.SourceGeneration}
			sealed, e := t.drain(key)
			if e != nil {
				return out, e
			}
			if !validInboxDrainResult(r, key, candidate, sealed) {
				return out, ErrEvidence
			}
			session, e := t.current()
			if e != nil {
				return out, e
			}
			if session.Revision < sealed.Cursor.Revision {
				return out, ErrEvidence
			}
			row.DrainAfterSourceID, row.DrainAfterSourceGeneration = candidate.Key.SourceID, candidate.Key.SourceGeneration
			row.ProgressRevision = session.Revision
			row, err = t.write(row)
			if err != nil {
				return out, err
			}
		}
		if !page.Done {
			return out, ErrSourceDrainPending
		}
		row.DrainDone = true
	}
	if row.Stage != meta.MQTTBindingRemoved {
		session, e := t.current()
		if e != nil {
			return out, e
		}
		row.ProgressRevision = session.Revision
		row.Stage, row.ReleaseReason = meta.MQTTBindingRemoved, meta.MQTTBindingDrained
		row, err = t.write(row)
		if err != nil {
			return out, err
		}
	}
	current, found, err := t.qualification()
	if err != nil {
		return out, err
	}
	if !found || current != row || !current.DrainDone || current.Stage != meta.MQTTBindingRemoved {
		return out, ErrEvidence
	}
	if _, err = t.current(); err != nil {
		return out, err
	}
	sub := r.Subscription
	return SubscriptionProjectionReceipt{Namespace: sub.Namespace, ClientID: sub.ClientID, Topic: sub.Topic, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation, IntentRevision: sub.Revision, OperationID: sub.OperationID}, nil
}

func inboxDrainCursor(row meta.MQTTSourceBinding) meta.MQTTDeliveryCursorKey {
	if row.DrainAfterSourceID == "" {
		return meta.MQTTDeliveryCursorKey{}
	}
	k := row.Key
	return meta.MQTTDeliveryCursorKey{Namespace: k.Namespace, ClientID: k.ClientID, SessionGeneration: k.SessionGeneration, SubscriptionGeneration: k.SubscriptionGeneration, SourceKind: meta.MQTTSourceChannel, SourceID: row.DrainAfterSourceID, SourceGeneration: row.DrainAfterSourceGeneration}
}

func validInboxDrainResult(r SubscriptionProjectionRequest, key meta.MQTTSourceBindingKey, before meta.MQTTDeliveryCursor, result SourceDrainResult) bool {
	b, c := result.Binding, result.Cursor
	return meta.ValidateMQTTSourceBinding(b) == nil && meta.ValidateMQTTDeliveryCursor(c) == nil && b.Key == key && b.UID == r.UID && b.Topic == r.Subscription.Topic && b.OperationID == r.Subscription.OperationID && b.IntentRevision == r.Subscription.Revision && b.AuthorizationVersion == 0 &&
		b.BoundaryKnown && b.EndKnown && b.Stage >= meta.MQTTBindingRemoving && (b.ReleaseReason == 0 || b.ReleaseReason == meta.MQTTBindingDrained) &&
		c.Key == before.Key && c.Topic == b.Topic && c.AuthorizationVersion == 0 && c.StartAfter == before.StartAfter && c.StartAfter == b.StartAfter && c.Revision >= before.Revision && c.Revision >= b.ProgressRevision && c.CompletedThrough >= b.CompletedThrough &&
		c.WindowThrough == b.EndThrough && c.AccountedThrough == b.EndThrough && c.PendingMessages == uint64(c.InflightCount) && c.PendingBytes == c.InflightBytes
}
