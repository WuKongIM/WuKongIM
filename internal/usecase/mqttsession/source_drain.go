package mqttsession

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// SourceDrainMetadata keeps sealing on the source Slot and window/accounting
// mutations on the Session Slot, both through current foreground authority.
type SourceDrainMetadata interface {
	SourceProgressMetadata
	MutateMQTTWindow(context.Context, meta.MQTTWindowMutation) (meta.MQTTWindowResult, error)
	MutateMQTTDeliveryCursor(context.Context, meta.MQTTDeliveryCursorMutation) (meta.MQTTDeliveryCursorResult, error)
}

type SourceDrainOptions struct {
	Store  SourceDrainMetadata
	Owners *runtime.Owners
	// Sources is needed only to fix an interrupted preparation's unknown start.
	Sources SourceProtector
	// Now shares the owner's monotonic clock; Timeout bounds all reads and writes.
	Now     func() time.Time
	Timeout time.Duration
}

// SourceDrain closes one durable source range without discarding inflight work.
// It owns no worker and does not advance native source or shared-content GC.
type SourceDrain struct {
	options SourceDrainOptions
	guard   *Subscriptions
}

type SourceDrainResult struct {
	Binding meta.MQTTSourceBinding
	Cursor  meta.MQTTDeliveryCursor
}

func NewSourceDrain(o SourceDrainOptions) (*SourceDrain, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Store == nil || o.Owners == nil || o.Timeout <= 0 || o.Timeout > time.Minute {
		return nil, ErrInvalid
	}
	p := &SourceDrain{options: o, guard: &Subscriptions{options: SubscriptionOptions{Owners: o.Owners, Now: o.Now, Timeout: o.Timeout}}}
	if _, err := p.guard.now(); err != nil {
		return nil, err
	}
	return p, nil
}

// Seal fixes the already-closed intent's durable accounting end before releasing
// only unadmitted counts/bytes. Outstanding exchanges remain independently ACKable.
// Lost replies resume from authoritative state; there is no internal retry loop.
func (p *SourceDrain) Seal(parent context.Context, o contract.Owner, key meta.MQTTSourceBindingKey) (out SourceDrainResult, err error) {
	q := meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: key}
	if p == nil || key.Owner.Kind != meta.MQTTBindingChannel || meta.ValidateMQTTRead(q) != nil ||
		key.Namespace != o.Key.Namespace || key.ClientID != o.Key.ClientID || key.SessionGeneration != o.SessionGeneration {
		return out, ErrInvalid
	}
	op, ctx, cancel, err := p.guard.begin(parent, o)
	if err != nil {
		return out, err
	}
	defer finishSubscription(op, cancel, &err)
	r, err := p.read(ctx, op, q)
	if err != nil {
		return out, err
	}
	if r.Session != nil || len(r.Subscriptions) != 0 || len(r.DeliveryCursors) != 0 || len(r.Bindings) != 1 {
		return out, ErrEvidence
	}
	b := r.Bindings[0]
	if meta.ValidateMQTTSourceBinding(b) != nil || b.Key != key || b.UID != op.UID() {
		return out, ErrEvidence
	}
	if b.ReleaseReason != 0 && b.ReleaseReason != meta.MQTTBindingDrained {
		return out, ErrConflict
	}
	s, sub, err := p.closedIntent(ctx, op, o, b)
	if err != nil {
		return out, err
	}
	s, cursor, found, err := p.cursor(ctx, op, o, b, s.Revision)
	if err != nil {
		return out, err
	}
	if !found {
		// Only unproven preparation may lack a cursor. A previously active or
		// sealed binding cannot turn missing durable state into an empty range.
		if b.ProgressRevision != 0 || b.Stage == meta.MQTTBindingActive || b.EndKnown {
			return out, ErrEvidence
		}
		if !b.BoundaryKnown {
			b, err = p.cancelBoundary(ctx, op, b, sub)
			if err != nil {
				return out, err
			}
		}
		s, cursor, err = p.cancelCursor(ctx, op, o, b, s)
		if err != nil {
			return out, err
		}
	}
	if !b.EndKnown {
		next := b
		next.Stage, next.IntentRevision = meta.MQTTBindingRemoving, sub.Revision
		next.EndKnown, next.EndThrough = true, cursor.AccountedThrough
		next.CompletedThrough, next.ProgressRevision = cursor.CompletedThrough, cursor.Revision
		b, err = p.sealBinding(ctx, op, b, next)
		if err != nil {
			return out, err
		}
	}
	if cursor.WindowThrough < b.EndThrough {
		now, e := p.guard.now()
		if e != nil {
			return out, e
		}
		if e = p.guard.checkSession(ctx, op, o, &s); e != nil {
			return out, e
		}
		m := meta.MQTTWindowMutation{Key: cursor.Key, ExpectedRevision: s.Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID,
			Op: meta.MQTTWindowAdvance, Through: b.EndThrough, ReleasedMessages: cursor.PendingMessages - uint64(cursor.InflightCount), ReleasedBytes: cursor.PendingBytes - cursor.InflightBytes, UpdatedAtMS: now.UnixMilli()}
		if meta.ValidateMQTTWindowMutation(m) != nil {
			return out, ErrEvidence
		}
		receipt, e := p.options.Store.MutateMQTTWindow(ctx, m)
		if e != nil {
			return out, e
		}
		if e = checkSubscriptionScope(ctx, op); e != nil {
			return out, e
		}
		if receipt.Status == meta.MQTTWindowConflict {
			return out, ErrConflict
		}
		if (receipt.Status != meta.MQTTWindowApplied && receipt.Status != meta.MQTTWindowUnchanged) || receipt.CurrentRevision != s.Revision+1 || receipt.PacketID != 0 || receipt.DeliveryOrder != 0 {
			return out, ErrEvidence
		}
		s.Revision = receipt.CurrentRevision
	}
	_, cursor, found, err = p.cursor(ctx, op, o, b, s.Revision)
	if err != nil {
		return out, err
	}
	if !found || cursor.WindowThrough != b.EndThrough || cursor.PendingMessages != uint64(cursor.InflightCount) || cursor.PendingBytes != cursor.InflightBytes {
		return out, ErrEvidence
	}
	return SourceDrainResult{Binding: b, Cursor: cursor}, nil
}

// closedIntent proves no future accounting/admission can enter this generation.
// Receive permission is deliberately irrelevant to releasing unadmitted backlog.
func (p *SourceDrain) closedIntent(ctx context.Context, op *subscriptionOperation, o contract.Owner, b meta.MQTTSourceBinding) (meta.MQTTSession, meta.MQTTSubscription, error) {
	var s meta.MQTTSession
	var sub meta.MQTTSubscription
	r, err := p.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: b.Key.Namespace, ClientID: b.Key.ClientID, SessionGeneration: b.Key.SessionGeneration, Topic: b.Topic})
	if err != nil {
		return s, sub, err
	}
	if err = p.guard.checkSession(ctx, op, o, r.Session); err != nil {
		return s, sub, err
	}
	if len(r.Bindings) != 0 || len(r.DeliveryCursors) != 0 || len(r.Subscriptions) != 1 || r.Session.Revision < b.IntentRevision || r.Session.Revision < b.ProgressRevision {
		return s, sub, ErrEvidence
	}
	sub = r.Subscriptions[0]
	if !validSubscriptionEvidence(sub, *r.Session) || sub.Topic != b.Topic || sub.Generation < b.Key.SubscriptionGeneration || sub.Revision < b.IntentRevision {
		return s, meta.MQTTSubscription{}, ErrEvidence
	}
	if sub.Generation == b.Key.SubscriptionGeneration {
		if sub.OperationID != b.OperationID || sub.AuthorizationVersion != b.AuthorizationVersion ||
			(sub.TargetKind == meta.MQTTSubscriptionGroup && b.Key.Owner.ID != "2:"+sub.TargetID) ||
			(sub.TargetKind == meta.MQTTSubscriptionUserInbox && sub.TargetID != op.UID()) {
			return s, meta.MQTTSubscription{}, ErrEvidence
		}
		if sub.Stage < meta.MQTTSubscriptionRemoving {
			return s, meta.MQTTSubscription{}, ErrConflict
		}
	} else if sub.OperationID == b.OperationID {
		return s, meta.MQTTSubscription{}, ErrEvidence
	}
	return *r.Session, sub, nil
}

func (p *SourceDrain) cursor(ctx context.Context, op *subscriptionOperation, o contract.Owner, b meta.MQTTSourceBinding, minimum uint64) (meta.MQTTSession, meta.MQTTDeliveryCursor, bool, error) {
	var s meta.MQTTSession
	var c meta.MQTTDeliveryCursor
	k := b.Key
	key := meta.MQTTDeliveryCursorKey{Namespace: k.Namespace, ClientID: k.ClientID, SessionGeneration: k.SessionGeneration, SubscriptionGeneration: k.SubscriptionGeneration, SourceKind: meta.MQTTSourceChannel, SourceID: k.Owner.ID, SourceGeneration: k.Owner.Generation}
	r, err := p.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: key})
	if err != nil {
		return s, c, false, err
	}
	if err = p.guard.checkSession(ctx, op, o, r.Session); err != nil {
		return s, c, false, err
	}
	if len(r.Bindings) != 0 || len(r.Subscriptions) != 0 || len(r.DeliveryCursors) > 1 || r.Session.Revision < minimum {
		return s, c, false, ErrEvidence
	}
	if len(r.DeliveryCursors) == 0 {
		return *r.Session, c, false, nil
	}
	c = r.DeliveryCursors[0]
	if !b.BoundaryKnown || meta.ValidateMQTTDeliveryCursor(c) != nil || c.Key != key || c.Topic != b.Topic || c.AuthorizationVersion != b.AuthorizationVersion ||
		c.Revision > r.Session.Revision || c.Revision < b.ProgressRevision || c.StartAfter != b.StartAfter || c.CompletedThrough < b.CompletedThrough ||
		(b.EndKnown && c.AccountedThrough != b.EndThrough) {
		return s, meta.MQTTDeliveryCursor{}, false, ErrEvidence
	}
	return *r.Session, c, true, nil
}

// cancelBoundary confirms replicated protection before choosing one empty start
// for an unknown preparation. Closed admission makes that choice non-delivering.
func (p *SourceDrain) cancelBoundary(ctx context.Context, op *subscriptionOperation, b meta.MQTTSourceBinding, sub meta.MQTTSubscription) (meta.MQTTSourceBinding, error) {
	typeText, id, found := strings.Cut(b.Key.Owner.ID, ":")
	kind, parseErr := strconv.ParseUint(typeText, 10, 8)
	if !found || parseErr != nil || kind == 0 || strconv.FormatUint(kind, 10) != typeText || !contract.ValidIdentity(id, 1024) || p.options.Sources == nil {
		return meta.MQTTSourceBinding{}, ErrEvidence
	}
	if err := checkSubscriptionScope(ctx, op); err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	channel := SourceChannel{ID: id, Type: uint8(kind)}
	source, err := p.options.Sources.ProtectMQTTSource(ctx, channel)
	if err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	if source.Channel != channel || source.Generation != b.Key.Owner.Generation || source.ProtectedAfter >= source.CommittedThrough || b.Revision == math.MaxUint64 {
		return meta.MQTTSourceBinding{}, ErrEvidence
	}
	next := b
	next.Stage, next.IntentRevision, next.BoundaryKnown = meta.MQTTBindingRemoving, sub.Revision, true
	next.StartAfter, next.CompletedThrough = source.CommittedThrough, source.CommittedThrough
	next.ProtectionRevision = b.Revision + 1
	return p.sealBinding(ctx, op, b, next)
}

func (p *SourceDrain) cancelCursor(ctx context.Context, op *subscriptionOperation, o contract.Owner, b meta.MQTTSourceBinding, s meta.MQTTSession) (meta.MQTTSession, meta.MQTTDeliveryCursor, error) {
	var empty meta.MQTTDeliveryCursor
	if err := p.guard.checkSession(ctx, op, o, &s); err != nil {
		return meta.MQTTSession{}, empty, err
	}
	now, err := p.guard.now()
	if err != nil {
		return meta.MQTTSession{}, empty, err
	}
	k := b.Key
	m := meta.MQTTDeliveryCursorMutation{Key: meta.MQTTDeliveryCursorKey{Namespace: k.Namespace, ClientID: k.ClientID, SessionGeneration: k.SessionGeneration, SubscriptionGeneration: k.SubscriptionGeneration, SourceKind: meta.MQTTSourceChannel, SourceID: k.Owner.ID, SourceGeneration: k.Owner.Generation},
		ExpectedRevision: s.Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID,
		Op: meta.MQTTCursorCancelInit, Topic: b.Topic, AuthorizationVersion: b.AuthorizationVersion, Through: b.StartAfter, UpdatedAtMS: now.UnixMilli()}
	if meta.ValidateMQTTDeliveryCursorMutation(m) != nil {
		return meta.MQTTSession{}, empty, ErrEvidence
	}
	r, err := p.options.Store.MutateMQTTDeliveryCursor(ctx, m)
	if err != nil {
		return meta.MQTTSession{}, empty, err
	}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return meta.MQTTSession{}, empty, err
	}
	if r.Status == meta.MQTTSessionCASConflict {
		return meta.MQTTSession{}, empty, ErrConflict
	}
	if (r.Status != meta.MQTTSessionCASApplied && r.Status != meta.MQTTSessionCASUnchanged) || r.CurrentRevision != s.Revision+1 || r.SessionState != meta.MQTTSessionActive || r.TerminationReason != 0 {
		return meta.MQTTSession{}, empty, ErrEvidence
	}
	s, c, found, err := p.cursor(ctx, op, o, b, r.CurrentRevision)
	if err != nil {
		return meta.MQTTSession{}, empty, err
	}
	if !found || c.Revision < r.CurrentRevision {
		return meta.MQTTSession{}, empty, ErrEvidence
	}
	return s, c, nil
}

func (p *SourceDrain) read(ctx context.Context, op *subscriptionOperation, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	if err := checkSubscriptionScope(ctx, op); err != nil {
		return meta.MQTTReadResult{}, err
	}
	r, err := p.options.Store.ReadMQTT(ctx, q)
	if err != nil {
		return meta.MQTTReadResult{}, err
	}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return meta.MQTTReadResult{}, err
	}
	if !r.Done || r.After != (meta.MQTTReadCursor{}) || len(r.SourceOwners) != 0 || len(r.Sessions) != 0 || len(r.Inflight) != 0 || len(r.Wills) != 0 {
		return meta.MQTTReadResult{}, ErrEvidence
	}
	return r, nil
}

func (p *SourceDrain) sealBinding(ctx context.Context, op *subscriptionOperation, old, next meta.MQTTSourceBinding) (meta.MQTTSourceBinding, error) {
	now, err := p.guard.now()
	if err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	if old.Revision == math.MaxUint64 || now.UnixMilli() < old.UpdatedAtMS {
		return meta.MQTTSourceBinding{}, ErrClock
	}
	next.Revision, next.UpdatedAtMS, next.RecoveryAtMS = old.Revision+1, now.UnixMilli(), now.UnixMilli()
	if meta.ValidateMQTTSourceBinding(next) != nil {
		return meta.MQTTSourceBinding{}, ErrEvidence
	}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	r, err := p.options.Store.CompareAndSwapMQTTSourceBinding(ctx, old.Revision, next)
	if err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	if r.Status == meta.MQTTSessionCASConflict {
		return meta.MQTTSourceBinding{}, ErrConflict
	}
	if (r.Status != meta.MQTTSessionCASApplied && r.Status != meta.MQTTSessionCASUnchanged) || r.CurrentRevision != next.Revision {
		return meta.MQTTSourceBinding{}, ErrEvidence
	}
	return next, nil
}
