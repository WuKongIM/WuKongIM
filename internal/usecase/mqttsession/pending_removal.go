package mqttsession

import (
	"context"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// SubscriptionRemovalOptions requires both real projection paths; product
// composition must not silently leave one target kind without recovery.
type SubscriptionRemovalOptions struct {
	Store  SubscriptionMetadata
	Inbox  *InboxRemoval
	Groups *SourceDrain
	// Timeout bounds a full turn, default/maximum five seconds. Now must not regress.
	Timeout time.Duration
	Now     func() time.Time
}

// SubscriptionRemoval finishes one durable Removing intent without a live
// Owner. It does not activate Preparing work or reclaim ended-lifetime records.
type SubscriptionRemoval struct {
	options SubscriptionRemovalOptions
	guard   *Subscriptions
}

func NewSubscriptionRemoval(o SubscriptionRemovalOptions) (*SubscriptionRemoval, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Store == nil || o.Inbox == nil || o.Groups == nil || o.Timeout <= 0 || o.Timeout > 5*time.Second {
		return nil, ErrInvalid
	}
	p := &SubscriptionRemoval{options: o, guard: &Subscriptions{options: SubscriptionOptions{Now: o.Now, Timeout: o.Timeout}}}
	if _, err := p.guard.now(); err != nil {
		return nil, err
	}
	return p, nil
}

// Reconcile treats the recovery cursor only as a body-free primary-key hint.
// It captures current authority once, preserves inflight work, and finishes only
// after projection plus a fresh exact-child/Owner check. RecoveryAtMS is ignored.
func (p *SubscriptionRemoval) Reconcile(parent context.Context, hint meta.MQTTSubscriptionRecoveryCursor) (completed bool, err error) {
	q := meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: hint.Namespace, ClientID: hint.ClientID, SessionGeneration: hint.SessionGeneration, Topic: hint.Topic}
	if p == nil || parent == nil || meta.ValidateMQTTRead(q) != nil {
		return false, ErrInvalid
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
			completed = false
		}
	}()
	session, sub, found, err := p.read(ctx, q)
	if err != nil || !found {
		return false, err
	}
	if session.Generation != hint.SessionGeneration || session.State == meta.MQTTSessionEnded {
		return false, nil
	}
	owner := sessionOwner(session)
	scope := &closedIntentScope{uid: session.UID}
	if err = scope.checkSession(ctx, p.guard, owner, &session); err != nil {
		return false, err
	}
	if !validSubscriptionEvidence(sub, session) || sub.OperationID != subscriptionOperationID(sub) {
		return false, ErrEvidence
	}
	if sub.Stage == meta.MQTTSubscriptionRemoved {
		return true, nil
	}
	if sub.Stage != meta.MQTTSubscriptionRemoving {
		return false, nil
	}
	request := SubscriptionProjectionRequest{Owner: owner, UID: session.UID, Subscription: sub}
	switch sub.TargetKind {
	case meta.MQTTSubscriptionUserInbox:
		var receipt SubscriptionProjectionReceipt
		receipt, err = p.options.Inbox.RemoveClosed(ctx, request)
		if err == nil && receipt != (SubscriptionProjectionReceipt{Namespace: sub.Namespace, ClientID: sub.ClientID, Topic: sub.Topic, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation, IntentRevision: sub.Revision, OperationID: sub.OperationID}) {
			err = ErrEvidence
		}
	case meta.MQTTSubscriptionGroup:
		_, err = p.options.Groups.SealClosedGroup(ctx, request)
	default:
		err = ErrEvidence
	}
	if err != nil {
		return false, err
	}
	session, current, found, err := p.read(ctx, q)
	if err != nil {
		return false, err
	}
	if err = scope.checkSession(ctx, p.guard, owner, &session); err != nil {
		return false, err
	}
	if !found || current != sub || !validSubscriptionEvidence(current, session) {
		return false, ErrConflict
	}
	now, err := p.guard.now()
	if err != nil {
		return false, err
	}
	if session.Revision == math.MaxUint64 || now.UnixMilli() < session.UpdatedAtMS {
		return false, ErrClock
	}
	sub.Stage, sub.RecoveryAtMS = meta.MQTTSubscriptionRemoved, 0
	sub.Revision, sub.UpdatedAtMS = session.Revision+1, now.UnixMilli()
	mutation := meta.MQTTSubscriptionMutation{ExpectedRevision: session.Revision, OwnerGeneration: owner.OwnerGeneration, OwnerNodeID: owner.NodeID, OwnerBootID: owner.BootID, ConnectionID: owner.ConnectionID, Subscription: sub}
	if meta.ValidateMQTTSubscriptionMutation(mutation) != nil {
		return false, ErrEvidence
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	receipt, err := p.options.Store.MutateMQTTSubscription(ctx, mutation)
	if err != nil {
		return false, err
	}
	if receipt.Status == meta.MQTTSessionCASConflict {
		return false, ErrConflict
	}
	if (receipt.Status != meta.MQTTSessionCASApplied && receipt.Status != meta.MQTTSessionCASUnchanged) || receipt.CurrentRevision != sub.Revision {
		return false, ErrEvidence
	}
	return true, nil
}

func (p *SubscriptionRemoval) read(ctx context.Context, q meta.MQTTRead) (session meta.MQTTSession, sub meta.MQTTSubscription, found bool, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	r, err := p.options.Store.ReadMQTT(ctx, q)
	if err != nil {
		return
	}
	if err = ctx.Err(); err != nil {
		return
	}
	if !r.Done || r.After != (meta.MQTTReadCursor{}) || r.Runtime != nil || r.Admission != nil || r.Membership != nil || r.Accounting != nil || len(r.Sessions)+len(r.Directory)+len(r.SourceOwners)+len(r.DeliveryCursors)+len(r.Inflight)+len(r.Bindings)+len(r.Wills) != 0 || len(r.Subscriptions) > 1 {
		err = ErrEvidence
		return
	}
	if r.Session == nil {
		if len(r.Subscriptions) != 0 {
			err = ErrEvidence
		}
		return
	}
	if meta.ValidateMQTTSession(*r.Session) != nil || r.Session.Namespace != q.Namespace || r.Session.ClientID != q.ClientID {
		err = ErrEvidence
		return
	}
	session = *r.Session
	if len(r.Subscriptions) == 0 {
		return
	}
	sub = r.Subscriptions[0]
	if meta.ValidateMQTTSubscription(sub) != nil || sub.Namespace != q.Namespace || sub.ClientID != q.ClientID || sub.SessionGeneration != q.SessionGeneration || sub.Topic != q.Topic {
		err = ErrEvidence
		return
	}
	return session, sub, true, nil
}
