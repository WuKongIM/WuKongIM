package mqttsession

import (
	"context"
	"math"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// SourceChannel is the complete entry-neutral identity of one message log.
type SourceChannel struct {
	ID   string
	Type uint8
}

// ProtectedSource confirms current replicated protection. Its initial protection
// boundary and current committed tail are distinct; neither is a copy/GC proof.
type ProtectedSource struct {
	Channel                          SourceChannel
	Generation                       string
	ProtectedAfter, CommittedThrough uint64
}

// SourceProtector resolves fresh authority and installs/confirms replicated
// protection on all eligible log owners. It must not use replica-local state.
type SourceProtector interface {
	ProtectMQTTSource(context.Context, SourceChannel) (ProtectedSource, error)
}

// GroupSourceMetadata shares current-owner reads with subscription orchestration
// and adds exact source/Session commits. GroupSources never changes subscriptions.
type GroupSourceMetadata interface {
	SubscriptionMetadata
	CompareAndSwapMQTTSourceBinding(context.Context, uint64, meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error)
	MutateMQTTDeliveryCursor(context.Context, meta.MQTTDeliveryCursorMutation) (meta.MQTTDeliveryCursorResult, error)
}

type GroupSourceOptions struct {
	Store         GroupSourceMetadata
	Owners        *runtime.Owners
	Authorization SubscriptionAuthorizer
	Sources       SourceProtector
	// Now shares the owner's monotonic clock; Timeout bounds the entire operation.
	Now     func() time.Time
	Timeout time.Duration
}

// PreparedGroupSource proves only the source/cursor preparation step. It is not
// a SubscriptionProjectionReceipt, shared-copy proof or permission for SUBACK.
type PreparedGroupSource struct {
	Binding meta.MQTTSourceBinding
	Cursor  meta.MQTTDeliveryCursor
}

// GroupSources coordinates durable preparation without workers, scans over
// subscribers, message copies, delivery, subscription activation or cleanup.
type GroupSources struct {
	options GroupSourceOptions
	// guard reuses owner, clock and exact-intent checks without invoking transitions.
	guard *Subscriptions
}

func NewGroupSources(o GroupSourceOptions) (*GroupSources, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Store == nil || o.Owners == nil || o.Authorization == nil || o.Sources == nil || o.Timeout <= 0 || o.Timeout > time.Minute {
		return nil, ErrInvalid
	}
	p := &GroupSources{options: o, guard: &Subscriptions{options: SubscriptionOptions{Store: o.Store, Owners: o.Owners, Authorization: o.Authorization, Now: o.Now, Timeout: o.Timeout}}}
	if _, err := p.guard.now(); err != nil {
		return nil, err
	}
	return p, nil
}

// Prepare recovers one exact group intent and preserves its first durable
// consumption boundary through ambiguous commits and same-lifetime owner resume.
func (p *GroupSources) Prepare(parent context.Context, o contract.Owner, topic string) (out PreparedGroupSource, err error) {
	if p == nil || !validSubscriptionTopic(topic) {
		return out, ErrInvalid
	}
	op, ctx, cancel, err := p.guard.begin(parent, o)
	if err != nil {
		return out, err
	}
	defer finishSubscription(op, cancel, &err)
	_, sub, found, err := p.guard.read(ctx, op, o, topic)
	if err != nil {
		return out, err
	}
	if !found || sub.TargetKind != meta.MQTTSubscriptionGroup || (sub.Stage != meta.MQTTSubscriptionPreparing && sub.Stage != meta.MQTTSubscriptionActive) {
		return out, ErrConflict
	}
	return p.prepare(ctx, &preparationScope{live: op, uid: op.UID()}, o, sub)
}

// prepare shares protected boundary/cursor work without creating a live Owner
// scope for offline recovery. The caller pins the complete original intent.
func (p *GroupSources) prepare(ctx context.Context, op *preparationScope, o contract.Owner, sub meta.MQTTSubscription) (out PreparedGroupSource, err error) {
	if _, err = p.current(ctx, op, o, sub); err != nil {
		return out, err
	}
	if err = p.authorize(ctx, op, sub); err != nil {
		return out, err
	}
	channel := SourceChannel{ID: sub.TargetID, Type: 2}
	source, err := p.protect(ctx, op, channel)
	if err != nil {
		return out, err
	}
	key := meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: "2:" + channel.ID, Generation: source.Generation}, Namespace: sub.Namespace, ClientID: sub.ClientID, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation}
	cursorKey := meta.MQTTDeliveryCursorKey{Namespace: sub.Namespace, ClientID: sub.ClientID, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation, SourceKind: meta.MQTTSourceChannel, SourceID: key.Owner.ID, SourceGeneration: source.Generation}
	_, existing, hasCursor, err := p.readCursor(ctx, op, o, sub, cursorKey)
	if err != nil {
		return out, err
	}
	binding, found, err := p.readBinding(ctx, op, sub, key)
	if err != nil {
		return out, err
	}
	if !found {
		if hasCursor {
			return out, ErrEvidence
		}
		binding = meta.MQTTSourceBinding{Key: key, UID: op.UID(), Topic: sub.Topic, IntentRevision: sub.Revision, AuthorizationVersion: sub.AuthorizationVersion, OperationID: sub.OperationID, Stage: meta.MQTTBindingPreparing}
		binding, err = p.writeBinding(ctx, op, o, sub, binding)
		if err != nil {
			return out, err
		}
	}
	if !binding.BoundaryKnown {
		if hasCursor || binding.Stage != meta.MQTTBindingPreparing {
			return out, ErrEvidence
		}
		// Unknown responsibility is durable before a tail may become a cursor.
		confirmed, e := p.protect(ctx, op, channel)
		if e != nil {
			return out, e
		}
		if confirmed.Generation != source.Generation || confirmed.ProtectedAfter != source.ProtectedAfter || confirmed.CommittedThrough < source.CommittedThrough {
			return out, ErrEvidence
		}
		source = confirmed
		binding.BoundaryKnown = true
		binding.StartAfter, binding.CompletedThrough = source.CommittedThrough, source.CommittedThrough
		if binding.Revision == math.MaxUint64 {
			return out, ErrEvidence
		}
		binding.ProtectionRevision = binding.Revision + 1
		binding, err = p.writeBinding(ctx, op, o, sub, binding)
		if err != nil {
			return out, err
		}
	}
	if binding.StartAfter < source.ProtectedAfter || binding.StartAfter > source.CommittedThrough {
		return out, ErrEvidence
	}
	if hasCursor && existing.StartAfter != binding.StartAfter {
		return out, ErrEvidence
	}
	_, cursor, hasCursor, err := p.readCursor(ctx, op, o, sub, cursorKey)
	if err != nil {
		return out, err
	}
	if !hasCursor {
		if binding.Stage == meta.MQTTBindingActive {
			return out, ErrEvidence
		}
		cursor, err = p.initCursor(ctx, op, o, sub, cursorKey, binding.StartAfter)
		if err != nil {
			return out, err
		}
	}
	if cursor.StartAfter != binding.StartAfter || cursor.CompletedThrough < binding.CompletedThrough || cursor.Revision < binding.ProgressRevision {
		return out, ErrEvidence
	}
	if _, err = p.current(ctx, op, o, sub); err != nil {
		return out, err
	}
	if err = p.authorize(ctx, op, sub); err != nil {
		return out, err
	}
	if binding.Stage == meta.MQTTBindingPreparing {
		binding.Stage = meta.MQTTBindingActive
		binding.ProgressRevision = cursor.Revision
		binding, err = p.writeBinding(ctx, op, o, sub, binding)
		if err != nil {
			return out, err
		}
	}
	if _, err = p.current(ctx, op, o, sub); err != nil {
		return out, err
	}
	if err = p.authorize(ctx, op, sub); err != nil {
		return out, err
	}
	return PreparedGroupSource{Binding: binding, Cursor: cursor}, nil
}

func (p *GroupSources) current(ctx context.Context, op *preparationScope, o contract.Owner, sub meta.MQTTSubscription) (meta.MQTTSession, error) {
	return op.current(ctx, p.guard, p.options.Store, o, sub)
}

func (p *GroupSources) authorize(ctx context.Context, op *preparationScope, sub meta.MQTTSubscription) error {
	return op.authorize(ctx, p.guard, sub)
}

func (p *GroupSources) protect(ctx context.Context, op *preparationScope, channel SourceChannel) (ProtectedSource, error) {
	if err := op.check(ctx); err != nil {
		return ProtectedSource{}, err
	}
	source, err := p.options.Sources.ProtectMQTTSource(ctx, channel)
	if err != nil {
		return ProtectedSource{}, err
	}
	if err = op.check(ctx); err != nil {
		return ProtectedSource{}, err
	}
	if source.Channel != channel || !contract.ValidIdentity(source.Generation, 128) || source.ProtectedAfter >= source.CommittedThrough {
		return ProtectedSource{}, ErrEvidence
	}
	return source, nil
}

func (p *GroupSources) readBinding(ctx context.Context, op *preparationScope, sub meta.MQTTSubscription, key meta.MQTTSourceBindingKey) (meta.MQTTSourceBinding, bool, error) {
	if err := op.check(ctx); err != nil {
		return meta.MQTTSourceBinding{}, false, err
	}
	r, err := p.options.Store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: key})
	if err != nil {
		return meta.MQTTSourceBinding{}, false, err
	}
	if err = op.check(ctx); err != nil {
		return meta.MQTTSourceBinding{}, false, err
	}
	if !r.Done || len(r.Bindings) > 1 {
		return meta.MQTTSourceBinding{}, false, ErrEvidence
	}
	if len(r.Bindings) == 0 {
		return meta.MQTTSourceBinding{}, false, nil
	}
	b := r.Bindings[0]
	if meta.ValidateMQTTSourceBinding(b) != nil || b.Key != key || b.UID != op.UID() || b.Topic != sub.Topic || b.OperationID != sub.OperationID || b.AuthorizationVersion != sub.AuthorizationVersion || b.IntentRevision > sub.Revision {
		return meta.MQTTSourceBinding{}, false, ErrEvidence
	}
	if b.Stage != meta.MQTTBindingPreparing && b.Stage != meta.MQTTBindingActive {
		return meta.MQTTSourceBinding{}, false, ErrConflict
	}
	return b, true, nil
}

func (p *GroupSources) readCursor(ctx context.Context, op *preparationScope, o contract.Owner, sub meta.MQTTSubscription, key meta.MQTTDeliveryCursorKey) (meta.MQTTSession, meta.MQTTDeliveryCursor, bool, error) {
	var empty meta.MQTTDeliveryCursor
	if err := op.check(ctx); err != nil {
		return meta.MQTTSession{}, empty, false, err
	}
	r, err := p.options.Store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: sub.Namespace, ClientID: sub.ClientID, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation, Limit: 2})
	if err != nil {
		return meta.MQTTSession{}, empty, false, err
	}
	if err = op.checkSession(ctx, p.guard, o, r.Session); err != nil {
		return meta.MQTTSession{}, empty, false, err
	}
	if err = op.check(ctx); err != nil {
		return meta.MQTTSession{}, empty, false, err
	}
	if !r.Done || len(r.DeliveryCursors) > 1 || r.After != (meta.MQTTReadCursor{}) {
		return meta.MQTTSession{}, empty, false, ErrEvidence
	}
	if len(r.DeliveryCursors) == 0 {
		return *r.Session, empty, false, nil
	}
	cursor := r.DeliveryCursors[0]
	if meta.ValidateMQTTDeliveryCursor(cursor) != nil || cursor.Key != key || cursor.Topic != sub.Topic || cursor.AuthorizationVersion != sub.AuthorizationVersion || cursor.Revision > r.Session.Revision {
		return meta.MQTTSession{}, empty, false, ErrEvidence
	}
	return *r.Session, cursor, true, nil
}

func (p *GroupSources) writeBinding(ctx context.Context, op *preparationScope, o contract.Owner, sub meta.MQTTSubscription, row meta.MQTTSourceBinding) (meta.MQTTSourceBinding, error) {
	if _, err := p.current(ctx, op, o, sub); err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	if row.Revision == math.MaxUint64 {
		return meta.MQTTSourceBinding{}, ErrEvidence
	}
	now, err := p.guard.now()
	if err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	if now.UnixMilli() < row.UpdatedAtMS {
		return meta.MQTTSourceBinding{}, ErrClock
	}
	expected := row.Revision
	row.Revision++
	row.IntentRevision = sub.Revision
	row.UpdatedAtMS = now.UnixMilli()
	row.RecoveryAtMS = row.UpdatedAtMS
	if meta.ValidateMQTTSourceBinding(row) != nil {
		return meta.MQTTSourceBinding{}, ErrEvidence
	}
	if err = op.check(ctx); err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	receipt, err := p.options.Store.CompareAndSwapMQTTSourceBinding(ctx, expected, row)
	if err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	if err = op.check(ctx); err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	if receipt.Status == meta.MQTTSessionCASConflict {
		return meta.MQTTSourceBinding{}, ErrConflict
	}
	if (receipt.Status != meta.MQTTSessionCASApplied && receipt.Status != meta.MQTTSessionCASUnchanged) || receipt.CurrentRevision != row.Revision {
		return meta.MQTTSourceBinding{}, ErrEvidence
	}
	return row, nil
}
