package mqttsession

import (
	"context"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// A turn retains only one bounded page and its exact subscription intent.
type inboxEstablishmentTurn struct {
	p       *InboxEstablishment
	ctx     context.Context
	op      *subscriptionOperation
	request SubscriptionProjectionRequest
}

func (t *inboxEstablishmentTurn) key() meta.MQTTSourceBindingKey {
	sub := t.request.Subscription
	return meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingUID, ID: t.request.UID}, Namespace: sub.Namespace, ClientID: sub.ClientID, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation}
}

func (t *inboxEstablishmentTurn) read(q meta.MQTTRead) (meta.MQTTReadResult, error) {
	if err := checkSubscriptionScope(t.ctx, t.op); err != nil {
		return meta.MQTTReadResult{}, err
	}
	r, err := t.p.options.Store.ReadMQTT(t.ctx, q)
	if stopped := checkSubscriptionScope(t.ctx, t.op); stopped != nil {
		return meta.MQTTReadResult{}, stopped
	}
	if err != nil {
		return meta.MQTTReadResult{}, err
	}
	if r.Membership != nil || r.Accounting != nil || r.Admission != nil || len(r.Sessions)+len(r.DeliveryCursors)+len(r.Inflight)+len(r.SourceOwners)+len(r.Wills) != 0 ||
		(q.Kind != meta.MQTTReadSubscription && (r.Session != nil || len(r.Subscriptions) != 0)) || len(r.Subscriptions) > 1 ||
		(q.Kind != meta.MQTTReadSourceBinding && len(r.Bindings) != 0) || len(r.Bindings) > 1 ||
		(q.Kind != meta.MQTTReadInboxDirectory && (len(r.Directory) != 0 || !r.Done || r.After != (meta.MQTTReadCursor{}))) {
		return meta.MQTTReadResult{}, ErrEvidence
	}
	return r, nil
}

// current uses one coherent Session/child read and fresh authorization. It never
// substitutes a successor intent merely because generation or operation matches.
func (t *inboxEstablishmentTurn) current() error {
	o, sub := t.request.Owner, t.request.Subscription
	r, err := t.read(meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: o.Key.Namespace, ClientID: o.Key.ClientID, SessionGeneration: o.SessionGeneration, Topic: sub.Topic})
	if err != nil {
		return err
	}
	if err = t.p.guard.checkSession(t.ctx, t.op, o, r.Session); err != nil {
		return err
	}
	if len(r.Subscriptions) != 1 || r.Subscriptions[0] != sub || !validSubscriptionEvidence(sub, *r.Session) {
		return ErrConflict
	}
	version, err := t.p.guard.authorize(t.ctx, t.op, subscriptionRequestFromRow(sub))
	if err != nil {
		return err
	}
	if version != 0 {
		return ErrSubscriptionRevoked
	}
	return nil
}

func (t *inboxEstablishmentTurn) qualification() (meta.MQTTSourceBinding, bool, error) {
	r, err := t.read(meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: t.key()})
	if err != nil || len(r.Bindings) == 0 {
		return meta.MQTTSourceBinding{}, false, err
	}
	b, sub := r.Bindings[0], t.request.Subscription
	if meta.ValidateMQTTSourceBinding(b) != nil || b.Key != t.key() || b.UID != t.request.UID || b.Topic != sub.Topic || b.OperationID != sub.OperationID || b.AuthorizationVersion != 0 || b.IntentRevision > sub.Revision {
		return meta.MQTTSourceBinding{}, false, ErrEvidence
	}
	if b.Stage >= meta.MQTTBindingRemoving {
		return meta.MQTTSourceBinding{}, false, ErrConflict
	}
	now, err := t.p.guard.now()
	if err != nil {
		return meta.MQTTSourceBinding{}, false, err
	}
	if now.UnixMilli() < b.UpdatedAtMS {
		return meta.MQTTSourceBinding{}, false, ErrClock
	}
	return b, true, nil
}

func (t *inboxEstablishmentTurn) write(row meta.MQTTSourceBinding) (meta.MQTTSourceBinding, error) {
	if err := t.current(); err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	now, err := t.p.guard.now()
	if err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	if now.UnixMilli() < row.UpdatedAtMS || row.Revision == math.MaxUint64 {
		return meta.MQTTSourceBinding{}, ErrClock
	}
	expected := row.Revision
	row.Revision++
	row.IntentRevision = t.request.Subscription.Revision
	row.UpdatedAtMS, row.RecoveryAtMS = now.UnixMilli(), now.UnixMilli()
	if meta.ValidateMQTTSourceBinding(row) != nil {
		return meta.MQTTSourceBinding{}, ErrEvidence
	}
	if err := checkSubscriptionScope(t.ctx, t.op); err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	r, err := t.p.options.Store.CompareAndSwapMQTTSourceBinding(t.ctx, expected, row)
	if stopped := checkSubscriptionScope(t.ctx, t.op); stopped != nil {
		return meta.MQTTSourceBinding{}, stopped
	}
	if err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	if r.Status == meta.MQTTSessionCASConflict {
		return meta.MQTTSourceBinding{}, ErrConflict
	}
	if (r.Status != meta.MQTTSessionCASApplied && r.Status != meta.MQTTSessionCASUnchanged) || r.CurrentRevision != row.Revision {
		return meta.MQTTSourceBinding{}, ErrEvidence
	}
	return row, nil
}

func validateInboxEstablishmentPage(q meta.MQTTRead, r meta.MQTTReadResult) error {
	if len(r.Directory) > q.Limit || !r.Done && len(r.Directory) != q.Limit {
		return ErrEvidence
	}
	last := q.After.Directory
	for _, k := range r.Directory {
		witness := meta.MQTTRead{Kind: meta.MQTTReadInboxDirectory, Owner: q.Owner, Limit: 1, After: meta.MQTTReadCursor{Directory: k}}
		if meta.ValidateMQTTRead(witness) != nil || last != (meta.ChannelKey{}) && meta.CompareMQTTDirectoryKeys(last, k) >= 0 {
			return ErrEvidence
		}
		last = k
	}
	if r.After != (meta.MQTTReadCursor{Directory: last}) {
		return ErrEvidence
	}
	return nil
}
