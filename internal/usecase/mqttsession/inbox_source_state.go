package mqttsession

import (
	"context"
	"math"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// inboxSourcePreparation is request-owned; only the immutable subscription
// intent is retained between fresh authority reads. Owner replacement may advance
// the Session inside that lifetime; a changed intent requires another attempt.
type inboxSourcePreparation struct {
	p       *InboxSources
	ctx     context.Context
	key     meta.MQTTSourceBindingKey
	channel SourceChannel
	intent  meta.MQTTSubscription
}

func (t *inboxSourcePreparation) read(q meta.MQTTRead) (meta.MQTTReadResult, error) {
	if err := t.ctx.Err(); err != nil {
		return meta.MQTTReadResult{}, err
	}
	r, err := t.p.options.Store.ReadMQTT(t.ctx, q)
	if canceled := t.ctx.Err(); canceled != nil {
		return meta.MQTTReadResult{}, canceled
	}
	if err != nil {
		return meta.MQTTReadResult{}, err
	}
	if !r.Done || r.After != (meta.MQTTReadCursor{}) || r.Membership != nil || r.Accounting != nil || len(r.Directory)+len(r.SourceOwners)+len(r.Sessions)+len(r.Inflight)+len(r.Wills) != 0 ||
		(q.Kind != meta.MQTTReadSourceBinding && len(r.Bindings) != 0) || len(r.Bindings) > 1 ||
		(q.Kind != meta.MQTTReadSubscription && len(r.Subscriptions) != 0) || len(r.Subscriptions) > 1 ||
		(q.Kind != meta.MQTTReadDeliveryCursor && len(r.DeliveryCursors) != 0) || len(r.DeliveryCursors) > 1 ||
		(q.Kind == meta.MQTTReadSourceBinding && r.Session != nil) {
		return meta.MQTTReadResult{}, ErrEvidence
	}
	return r, nil
}

func (t *inboxSourcePreparation) session(s *meta.MQTTSession) error {
	if s == nil || meta.ValidateMQTTSession(*s) != nil || s.Namespace != t.key.Namespace || s.ClientID != t.key.ClientID || s.UID != t.key.Owner.ID || s.Generation < t.key.SessionGeneration {
		return ErrEvidence
	}
	if s.Generation > t.key.SessionGeneration || s.State == meta.MQTTSessionEnded {
		return errInboxIntentClosed
	}
	return nil
}

// current orders qualification before coherent Session/child evidence. Neither
// wall-clock expiry nor absence substitutes for durable closed intent.
func (t *inboxSourcePreparation) current() (meta.MQTTSession, error) {
	q, err := t.read(meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: t.key})
	if err != nil {
		return meta.MQTTSession{}, err
	}
	if len(q.Bindings) != 1 {
		return meta.MQTTSession{}, ErrEvidence
	}
	qualification := q.Bindings[0]
	if meta.ValidateMQTTSourceBinding(qualification) != nil || qualification.Key != t.key || qualification.UID != t.key.Owner.ID || qualification.AuthorizationVersion != 0 {
		return meta.MQTTSession{}, ErrEvidence
	}
	if qualification.Stage >= meta.MQTTBindingRemoving {
		return meta.MQTTSession{}, errInboxIntentClosed
	}
	r, err := t.read(meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: t.key.Namespace, ClientID: t.key.ClientID, SessionGeneration: t.key.SessionGeneration, Topic: qualification.Topic})
	if err != nil {
		return meta.MQTTSession{}, err
	}
	if err = t.session(r.Session); err != nil {
		return meta.MQTTSession{}, err
	}
	if len(r.Subscriptions) != 1 {
		return meta.MQTTSession{}, ErrEvidence
	}
	sub := r.Subscriptions[0]
	if !validSubscriptionEvidence(sub, *r.Session) || sub.Topic != qualification.Topic || sub.Generation < t.key.SubscriptionGeneration {
		return meta.MQTTSession{}, ErrEvidence
	}
	if sub.Generation > t.key.SubscriptionGeneration || sub.Stage >= meta.MQTTSubscriptionRemoving {
		return meta.MQTTSession{}, errInboxIntentClosed
	}
	if sub.TargetKind != meta.MQTTSubscriptionUserInbox || sub.TargetID != t.key.Owner.ID || sub.AuthorizationVersion != 0 || sub.OperationID != qualification.OperationID || qualification.IntentRevision > sub.Revision {
		return meta.MQTTSession{}, ErrEvidence
	}
	if t.intent != (meta.MQTTSubscription{}) && t.intent != sub {
		return meta.MQTTSession{}, ErrConflict
	}
	t.intent = sub
	return *r.Session, nil
}

func (t *inboxSourcePreparation) protect() (ProtectedSource, error) {
	if _, err := t.current(); err != nil {
		return ProtectedSource{}, err
	}
	source, err := t.p.options.Sources.ProtectMQTTSource(t.ctx, t.channel)
	if canceled := t.ctx.Err(); canceled != nil {
		return ProtectedSource{}, canceled
	}
	if err != nil {
		return ProtectedSource{}, err
	}
	if source.Channel != t.channel || !contract.ValidIdentity(source.Generation, 128) || source.ProtectedAfter >= source.CommittedThrough {
		return ProtectedSource{}, ErrEvidence
	}
	return source, nil
}

func (t *inboxSourcePreparation) binding(key meta.MQTTSourceBindingKey) (meta.MQTTSourceBinding, bool, error) {
	r, err := t.read(meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: key})
	if err != nil {
		return meta.MQTTSourceBinding{}, false, err
	}
	if len(r.Bindings) == 0 {
		return meta.MQTTSourceBinding{}, false, nil
	}
	b := r.Bindings[0]
	if meta.ValidateMQTTSourceBinding(b) != nil || b.Key != key || b.UID != t.key.Owner.ID || b.Topic != t.intent.Topic || b.OperationID != t.intent.OperationID || b.AuthorizationVersion != 0 || b.IntentRevision > t.intent.Revision {
		return meta.MQTTSourceBinding{}, false, ErrEvidence
	}
	if b.Stage >= meta.MQTTBindingRemoving {
		return meta.MQTTSourceBinding{}, false, ErrConflict
	}
	return b, true, nil
}

func (t *inboxSourcePreparation) cursor(key meta.MQTTDeliveryCursorKey) (meta.MQTTDeliveryCursor, bool, error) {
	r, err := t.read(meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: key})
	if err != nil {
		return meta.MQTTDeliveryCursor{}, false, err
	}
	if err = t.session(r.Session); err != nil {
		return meta.MQTTDeliveryCursor{}, false, err
	}
	if len(r.DeliveryCursors) == 0 {
		return meta.MQTTDeliveryCursor{}, false, nil
	}
	c := r.DeliveryCursors[0]
	if meta.ValidateMQTTDeliveryCursor(c) != nil || c.Key != key || c.Topic != t.intent.Topic || c.AuthorizationVersion != 0 || c.Revision > r.Session.Revision {
		return meta.MQTTDeliveryCursor{}, false, ErrEvidence
	}
	return c, true, nil
}

func (t *inboxSourcePreparation) writeBinding(row meta.MQTTSourceBinding) (meta.MQTTSourceBinding, error) {
	if _, err := t.current(); err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	now, err := t.p.now()
	if err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	if now < row.UpdatedAtMS || row.Revision == math.MaxUint64 {
		return meta.MQTTSourceBinding{}, ErrClock
	}
	expected := row.Revision
	row.Revision++
	row.IntentRevision = t.intent.Revision
	row.RecoveryAtMS, row.UpdatedAtMS = now, now
	if meta.ValidateMQTTSourceBinding(row) != nil {
		return meta.MQTTSourceBinding{}, ErrEvidence
	}
	if err = t.ctx.Err(); err != nil {
		return meta.MQTTSourceBinding{}, err
	}
	r, err := t.p.options.Store.CompareAndSwapMQTTSourceBinding(t.ctx, expected, row)
	if canceled := t.ctx.Err(); canceled != nil {
		return meta.MQTTSourceBinding{}, canceled
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

func (t *inboxSourcePreparation) initializeCursor(key meta.MQTTDeliveryCursorKey, start uint64) (uint64, error) {
	s, err := t.current()
	if err != nil {
		return 0, err
	}
	now, err := t.p.now()
	if err != nil {
		return 0, err
	}
	if now < s.UpdatedAtMS || s.Revision == math.MaxUint64 {
		return 0, ErrClock
	}
	m := meta.MQTTDeliveryCursorMutation{Key: key, ExpectedRevision: s.Revision, OwnerGeneration: s.OwnerGeneration, OwnerNodeID: s.OwnerNodeID, OwnerBootID: s.OwnerBootID, ConnectionID: s.ConnectionID, Op: meta.MQTTCursorInit, Topic: t.intent.Topic, Through: start, UpdatedAtMS: now}
	if meta.ValidateMQTTDeliveryCursorMutation(m) != nil {
		return 0, ErrEvidence
	}
	if err = t.ctx.Err(); err != nil {
		return 0, err
	}
	r, err := t.p.options.Store.MutateMQTTDeliveryCursor(t.ctx, m)
	if canceled := t.ctx.Err(); canceled != nil {
		return 0, canceled
	}
	if err != nil {
		return 0, err
	}
	if r.Status == meta.MQTTSessionCASConflict {
		return 0, ErrConflict
	}
	if (r.Status != meta.MQTTSessionCASApplied && r.Status != meta.MQTTSessionCASUnchanged) || r.CurrentRevision != s.Revision+1 || r.SessionState != s.State || r.TerminationReason != 0 {
		return 0, ErrEvidence
	}
	return r.CurrentRevision, nil
}
