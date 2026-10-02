package mqttsession

import (
	"context"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// preparationScope admits either live projection or recovery of one captured
// Offline intent. Offline preparation grants no execution or isolation proof.
type preparationScope struct {
	live *subscriptionOperation
	uid  string
}

type preparationReader interface {
	ReadMQTT(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
}

func (s *preparationScope) UID() string { return s.uid }

func (s *preparationScope) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.live != nil {
		return s.live.Check()
	}
	return nil
}

func (s *preparationScope) checkSession(ctx context.Context, guard *Subscriptions, owner contract.Owner, row *meta.MQTTSession) error {
	if s.live != nil {
		return guard.checkSession(ctx, s.live, owner, row)
	}
	if err := s.check(ctx); err != nil {
		return err
	}
	if row == nil {
		return ErrFenced
	}
	if meta.ValidateMQTTSession(*row) != nil {
		return ErrEvidence
	}
	if sessionOwner(*row) != owner || row.UID != s.uid || row.State != meta.MQTTSessionOffline {
		return ErrFenced
	}
	now, err := guard.now()
	if err != nil {
		return err
	}
	if now.UnixMilli() < row.UpdatedAtMS {
		return ErrClock
	}
	if now.UnixMilli() >= row.OfflineExpiresAtMS {
		return ErrFenced
	}
	return nil
}

// current pins the complete child, rather than adopting a replacement with the
// same key. Each caller uses fresh authoritative reads before its next effect.
func (s *preparationScope) current(ctx context.Context, guard *Subscriptions, store preparationReader, owner contract.Owner, sub meta.MQTTSubscription) (meta.MQTTSession, error) {
	if err := s.check(ctx); err != nil {
		return meta.MQTTSession{}, err
	}
	r, err := store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: owner.Key.Namespace, ClientID: owner.Key.ClientID, SessionGeneration: owner.SessionGeneration, Topic: sub.Topic})
	if stopped := s.check(ctx); stopped != nil {
		return meta.MQTTSession{}, stopped
	}
	if err != nil {
		return meta.MQTTSession{}, err
	}
	if !r.Done || r.After != (meta.MQTTReadCursor{}) || r.Runtime != nil || r.Admission != nil || r.Membership != nil || r.Accounting != nil || len(r.Sessions)+len(r.Directory)+len(r.SourceOwners)+len(r.DeliveryCursors)+len(r.Inflight)+len(r.Bindings)+len(r.Wills) != 0 || len(r.Subscriptions) > 1 {
		return meta.MQTTSession{}, ErrEvidence
	}
	if err = s.checkSession(ctx, guard, owner, r.Session); err != nil {
		return meta.MQTTSession{}, err
	}
	if len(r.Subscriptions) != 1 || r.Subscriptions[0] != sub || !validSubscriptionEvidence(sub, *r.Session) {
		return meta.MQTTSession{}, ErrConflict
	}
	return *r.Session, nil
}

func (s *preparationScope) authorize(ctx context.Context, guard *Subscriptions, sub meta.MQTTSubscription) error {
	if sub.TargetKind == meta.MQTTSubscriptionUserInbox && sub.TargetID != s.uid {
		return ErrSubscriptionDenied
	}
	if err := s.check(ctx); err != nil {
		return err
	}
	version, err := guard.options.Authorization.AuthorizeSubscription(ctx, s.uid, subscriptionRequestFromRow(sub))
	if stopped := s.check(ctx); stopped != nil {
		return stopped
	}
	if err != nil {
		return err
	}
	if version != sub.AuthorizationVersion {
		return ErrSubscriptionRevoked
	}
	return nil
}

func beginOfflinePreparation(parent context.Context, guard *Subscriptions, r SubscriptionProjectionRequest) (*preparationScope, context.Context, context.CancelFunc, error) {
	if parent == nil || guard == nil || r.Owner.Validate() != nil || !contract.ValidIdentity(r.UID, 1024) || meta.ValidateMQTTSubscription(r.Subscription) != nil {
		return nil, nil, nil, ErrInvalid
	}
	sub := r.Subscription
	if sub.Stage != meta.MQTTSubscriptionPreparing {
		return nil, nil, nil, ErrConflict
	}
	if sub.Namespace != r.Owner.Key.Namespace || sub.ClientID != r.Owner.Key.ClientID || sub.SessionGeneration != r.Owner.SessionGeneration {
		return nil, nil, nil, ErrEvidence
	}
	ctx, cancel := context.WithTimeout(parent, guard.options.Timeout)
	return &preparationScope{uid: r.UID}, ctx, cancel, nil
}

// A late or panicking dependency cannot mint a successful projection receipt.
func finishPreparation(ctx context.Context, scope *preparationScope, out *SubscriptionProjectionReceipt, err *error) {
	if recover() != nil {
		*err = ErrSubscriptionCallback
	}
	if stopped := scope.check(ctx); stopped != nil {
		*err = stopped
	}
	if *err != nil {
		*out = SubscriptionProjectionReceipt{}
	}
}
