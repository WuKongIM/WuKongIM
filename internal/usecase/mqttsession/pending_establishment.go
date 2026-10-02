package mqttsession

import (
	"context"
	"errors"
	"math"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// OfflineSubscriptionProjection prepares one frozen Offline intent, retaining
// source protection and all-replica recovery. Success never emits a wire reply.
type OfflineSubscriptionProjection interface {
	EstablishOffline(context.Context, SubscriptionProjectionRequest) (SubscriptionProjectionReceipt, error)
}

type SubscriptionEstablishmentOptions struct {
	// Store supplies authoritative Session/child reads and the final Slot CAS.
	Store SubscriptionMetadata
	// Inbox and Groups preserve source boundaries and all-replica confirmation.
	Inbox, Groups OfflineSubscriptionProjection
	// Authorization rechecks the frozen receive-permission incarnation.
	Authorization SubscriptionAuthorizer
	// Ender proves exact isolation before recording definite receive revocation.
	Ender SessionEnder
	// Timeout bounds one turn, default/maximum five seconds; Now cannot regress.
	Timeout time.Duration
	Now     func() time.Time
}

// SubscriptionEstablishmentResult reports confirmed observations, not unique
// subscriptions. Errors and late replies never retain either confirmation.
type SubscriptionEstablishmentResult struct{ Activated, RevokedEnded bool }

// SubscriptionEstablishment finishes an existing Offline Preparing intent. It
// creates neither subscriptions nor Owners and cannot follow a changed lifetime.
type SubscriptionEstablishment struct {
	options SubscriptionEstablishmentOptions
	guard   *Subscriptions
}

func NewSubscriptionEstablishment(o SubscriptionEstablishmentOptions) (*SubscriptionEstablishment, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Store == nil || o.Inbox == nil || o.Groups == nil || o.Authorization == nil || o.Ender == nil || o.Timeout <= 0 || o.Timeout > 5*time.Second {
		return nil, ErrInvalid
	}
	p := &SubscriptionEstablishment{options: o, guard: &Subscriptions{options: SubscriptionOptions{Now: o.Now, Timeout: o.Timeout, Authorization: o.Authorization}}}
	if _, err := p.guard.now(); err != nil {
		return nil, err
	}
	return p, nil
}

// Reconcile treats the hint as a primary key only. It pins one Owner and full
// child, confirms projection, rechecks permission and attempts one final CAS.
func (p *SubscriptionEstablishment) Reconcile(parent context.Context, hint meta.MQTTSubscriptionRecoveryCursor) (out SubscriptionEstablishmentResult, err error) {
	q := meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: hint.Namespace, ClientID: hint.ClientID, SessionGeneration: hint.SessionGeneration, Topic: hint.Topic}
	if p == nil || parent == nil || meta.ValidateMQTTRead(q) != nil {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, p.options.Timeout)
	defer cancel()
	defer func() {
		if recover() != nil {
			err = ErrSubscriptionCallback
		}
		if stopped := ctx.Err(); stopped != nil {
			err = stopped
		}
		if err != nil {
			out = SubscriptionEstablishmentResult{}
		}
	}()
	session, sub, found, err := readPendingSubscription(ctx, p.options.Store, q)
	if err != nil || !found {
		return out, err
	}
	if session.Generation != hint.SessionGeneration {
		return out, nil
	}
	if !validSubscriptionEvidence(sub, session) || sub.OperationID != subscriptionOperationID(sub) {
		return out, ErrEvidence
	}
	if sub.Stage != meta.MQTTSubscriptionPreparing && sub.Stage != meta.MQTTSubscriptionActive {
		return out, nil
	}
	owner := sessionOwner(session)
	if session.State == meta.MQTTSessionEnded {
		if session.TerminationReason != meta.MQTTSessionRevoked {
			return out, nil
		}
		// A prior ending may have committed without its reply. The End port must
		// still prove exact isolation; an Ended row alone cannot confirm it.
		err = p.options.Ender.End(ctx, EndCommand{Owner: owner, Reason: meta.MQTTSessionRevoked})
		return SubscriptionEstablishmentResult{RevokedEnded: err == nil}, err
	}
	if session.State != meta.MQTTSessionOffline {
		return out, nil
	}
	scope := &preparationScope{uid: session.UID}
	if err = scope.checkSession(ctx, p.guard, owner, &session); err != nil {
		return out, err
	}
	if err = scope.authorize(ctx, p.guard, sub); err != nil {
		return p.revoke(ctx, scope, owner, sub, err)
	}
	if sub.Stage == meta.MQTTSubscriptionActive {
		return SubscriptionEstablishmentResult{Activated: true}, nil
	}
	r := SubscriptionProjectionRequest{Owner: owner, UID: session.UID, Subscription: sub}
	projection := p.options.Groups
	if sub.TargetKind == meta.MQTTSubscriptionUserInbox {
		projection = p.options.Inbox
	}
	receipt, err := projection.EstablishOffline(ctx, r)
	if err != nil {
		return p.revoke(ctx, scope, owner, sub, err)
	}
	if receipt != (SubscriptionProjectionReceipt{Namespace: sub.Namespace, ClientID: sub.ClientID, Topic: sub.Topic, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation, IntentRevision: sub.Revision, OperationID: sub.OperationID}) {
		return out, ErrEvidence
	}
	session, err = scope.current(ctx, p.guard, p.options.Store, owner, sub)
	if err != nil {
		return out, err
	}
	if err = scope.authorize(ctx, p.guard, sub); err != nil {
		return p.revoke(ctx, scope, owner, sub, err)
	}
	now, err := p.guard.now()
	if err != nil {
		return out, err
	}
	if session.Revision == math.MaxUint64 || now.UnixMilli() < session.UpdatedAtMS {
		return out, ErrClock
	}
	// Authorization may take time even without cancellation. Recheck the frozen
	// Offline expiry before the metadata effect; never extend that deadline.
	if err = scope.checkSession(ctx, p.guard, owner, &session); err != nil {
		return out, err
	}
	sub.Stage, sub.RecoveryAtMS = meta.MQTTSubscriptionActive, 0
	sub.Revision, sub.UpdatedAtMS = session.Revision+1, now.UnixMilli()
	m := meta.MQTTSubscriptionMutation{ExpectedRevision: session.Revision, OwnerGeneration: owner.OwnerGeneration, OwnerNodeID: owner.NodeID, OwnerBootID: owner.BootID, ConnectionID: owner.ConnectionID, Subscription: sub}
	if meta.ValidateMQTTSubscriptionMutation(m) != nil {
		return out, ErrEvidence
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	// A failed final write must preserve both the pending index and the
	// original prepared boundary, even after inbox discovery has completed.
	// gofail: var wkMQTTSubscriptionEstablishmentBeforeCommit bool
	// if wkMQTTSubscriptionEstablishmentBeforeCommit {
	//  return out, context.DeadlineExceeded
	// }
	committed, err := p.options.Store.MutateMQTTSubscription(ctx, m)
	if err != nil {
		return out, err
	}
	if committed.Status == meta.MQTTSessionCASConflict {
		return out, ErrConflict
	}
	if (committed.Status != meta.MQTTSessionCASApplied && committed.Status != meta.MQTTSessionCASUnchanged) || committed.CurrentRevision != sub.Revision {
		return out, ErrEvidence
	}
	return SubscriptionEstablishmentResult{Activated: true}, nil
}

// revoke acts only on a definite receive decision while the same Offline intent
// remains current. Infrastructure errors cannot manufacture a revocation.
func (p *SubscriptionEstablishment) revoke(ctx context.Context, scope *preparationScope, owner contract.Owner, sub meta.MQTTSubscription, cause error) (SubscriptionEstablishmentResult, error) {
	if err := ctx.Err(); err != nil {
		return SubscriptionEstablishmentResult{}, err
	}
	if !errors.Is(cause, ErrSubscriptionDenied) && !errors.Is(cause, ErrSubscriptionRevoked) {
		return SubscriptionEstablishmentResult{}, cause
	}
	if _, err := scope.current(ctx, p.guard, p.options.Store, owner, sub); err != nil {
		return SubscriptionEstablishmentResult{}, err
	}
	err := p.options.Ender.End(ctx, EndCommand{Owner: owner, Reason: meta.MQTTSessionRevoked})
	return SubscriptionEstablishmentResult{RevokedEnded: err == nil}, err
}
