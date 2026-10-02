package mqttsession

import (
	"context"
	"math"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

type inboxRemovalTurn struct {
	p       *InboxRemoval
	ctx     context.Context
	op      *closedIntentScope
	request SubscriptionProjectionRequest
}

func (t *inboxRemovalTurn) key() meta.MQTTSourceBindingKey {
	s := t.request.Subscription
	return meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingUID, ID: t.request.UID}, Namespace: s.Namespace, ClientID: s.ClientID, SessionGeneration: s.SessionGeneration, SubscriptionGeneration: s.Generation}
}

func (t *inboxRemovalTurn) read(q meta.MQTTRead) (meta.MQTTReadResult, error) {
	if err := t.op.check(t.ctx); err != nil {
		return meta.MQTTReadResult{}, err
	}
	r, err := t.p.options.Store.ReadMQTT(t.ctx, q)
	if stopped := t.op.check(t.ctx); stopped != nil {
		return meta.MQTTReadResult{}, stopped
	}
	if err != nil {
		return meta.MQTTReadResult{}, err
	}
	if r.Runtime != nil || r.Membership != nil || r.Accounting != nil || r.Admission != nil || len(r.Sessions)+len(r.Directory)+len(r.SourceOwners)+len(r.Inflight)+len(r.Wills) != 0 ||
		(q.Kind != meta.MQTTReadSubscription && len(r.Subscriptions) != 0) || len(r.Subscriptions) > 1 ||
		(q.Kind != meta.MQTTReadSourceBinding && len(r.Bindings) != 0) || len(r.Bindings) > 1 ||
		(q.Kind == meta.MQTTReadSourceBinding && r.Session != nil) ||
		(q.Kind != meta.MQTTReadDeliveryCursors && (len(r.DeliveryCursors) != 0 || !r.Done || r.After != (meta.MQTTReadCursor{}))) {
		return meta.MQTTReadResult{}, ErrEvidence
	}
	return r, nil
}

// current validates closure and ownership without granting receive permission.
func (t *inboxRemovalTurn) current() (meta.MQTTSession, error) {
	o, sub := t.request.Owner, t.request.Subscription
	r, err := t.read(meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: o.Key.Namespace, ClientID: o.Key.ClientID, SessionGeneration: o.SessionGeneration, Topic: sub.Topic})
	if err != nil {
		return meta.MQTTSession{}, err
	}
	if err = t.op.checkSession(t.ctx, t.p.guard, o, r.Session); err != nil {
		return meta.MQTTSession{}, err
	}
	if len(r.Subscriptions) != 1 || r.Subscriptions[0] != sub || !validSubscriptionEvidence(sub, *r.Session) || sub.OperationID != subscriptionOperationID(sub) {
		return meta.MQTTSession{}, ErrConflict
	}
	return *r.Session, nil
}

func (t *inboxRemovalTurn) qualification() (meta.MQTTSourceBinding, bool, error) {
	r, err := t.read(meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: t.key()})
	if err != nil || len(r.Bindings) == 0 {
		return meta.MQTTSourceBinding{}, false, err
	}
	b, sub := r.Bindings[0], t.request.Subscription
	if meta.ValidateMQTTSourceBinding(b) != nil || b.Key != t.key() || b.UID != t.request.UID || b.Topic != sub.Topic || b.OperationID != sub.OperationID || b.AuthorizationVersion != 0 || b.IntentRevision > sub.Revision ||
		(b.ReleaseReason != 0 && b.ReleaseReason != meta.MQTTBindingDrained) || b.Stage == meta.MQTTBindingRemoved && (b.DrainVersion != 1 || !b.DrainDone) {
		return meta.MQTTSourceBinding{}, false, ErrEvidence
	}
	s, err := t.current()
	if err != nil {
		return meta.MQTTSourceBinding{}, false, err
	}
	if b.ProgressRevision > s.Revision {
		return meta.MQTTSourceBinding{}, false, ErrEvidence
	}
	return b, true, nil
}

func (t *inboxRemovalTurn) write(row meta.MQTTSourceBinding) (meta.MQTTSourceBinding, error) {
	s, err := t.current()
	if err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	if row.ProgressRevision > s.Revision {
		return meta.MQTTSourceBinding{}, ErrEvidence
	}
	now, err := t.p.guard.now()
	if err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	if row.Revision == math.MaxUint64 || now.UnixMilli() < row.UpdatedAtMS {
		return meta.MQTTSourceBinding{}, ErrClock
	}
	expected := row.Revision
	row.Revision++
	// Removed rows stay scheduled so tombstone retirement discovers them.
	row.UpdatedAtMS, row.RecoveryAtMS = now.UnixMilli(), now.UnixMilli()
	if meta.ValidateMQTTSourceBinding(row) != nil {
		return meta.MQTTSourceBinding{}, ErrEvidence
	}
	if err = t.op.check(t.ctx); err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	r, err := t.p.options.Store.CompareAndSwapMQTTSourceBinding(t.ctx, expected, row)
	if stopped := t.op.check(t.ctx); stopped != nil {
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

func (t *inboxRemovalTurn) validatePage(row meta.MQTTSourceBinding, q meta.MQTTRead, r meta.MQTTReadResult) error {
	if err := t.op.checkSession(t.ctx, t.p.guard, t.request.Owner, r.Session); err != nil {
		return err
	}
	if r.Session.Revision < t.request.Subscription.Revision || r.Session.Revision < row.ProgressRevision || len(r.DeliveryCursors) > q.Limit || !r.Done && len(r.DeliveryCursors) != q.Limit {
		return ErrEvidence
	}
	previous := q.After.Delivery
	for _, c := range r.DeliveryCursors {
		k := c.Key
		if meta.ValidateMQTTDeliveryCursor(c) != nil || k.Namespace != q.Namespace || k.ClientID != q.ClientID || k.SessionGeneration != q.SessionGeneration || k.SubscriptionGeneration != q.SubscriptionGeneration || k.SourceKind != meta.MQTTSourceChannel || c.Topic != row.Topic || c.AuthorizationVersion != 0 || c.Revision > r.Session.Revision || !strings.HasPrefix(k.SourceID, "1:") {
			return ErrEvidence
		}
		id := strings.TrimPrefix(k.SourceID, "1:")
		left, right, err := channelid.DecodePersonChannel(id)
		if err != nil || channelid.EncodePersonChannel(left, right) != id || left != row.UID && right != row.UID || previous != (meta.MQTTDeliveryCursorKey{}) && !inboxDrainKeyAfter(previous, k) {
			return ErrEvidence
		}
		previous = k
	}
	want := q.After
	if !r.Done {
		want.Delivery = previous
	}
	if r.After != want {
		return ErrEvidence
	}
	return nil
}

func inboxDrainKeyAfter(a, b meta.MQTTDeliveryCursorKey) bool {
	for _, pair := range [][2]string{{a.SourceID, b.SourceID}, {a.SourceGeneration, b.SourceGeneration}} {
		if len(pair[0]) != len(pair[1]) {
			return len(pair[0]) < len(pair[1])
		}
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return false
}

// drain retains the enclosing captured Owner fence before and after background
// source work. The drain port itself rereads source and closed intent authority.
func (t *inboxRemovalTurn) drain(key meta.MQTTSourceBindingKey) (SourceDrainResult, error) {
	if t.op.live != nil {
		return t.p.options.Drain.Seal(t.ctx, t.request.Owner, key)
	}
	return t.p.options.ClosedDrain.SealClosed(t.ctx, t.request.Owner, key)
}
