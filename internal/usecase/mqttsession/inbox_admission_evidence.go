package mqttsession

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func (p *InboxAdmission) read(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	if err := ctx.Err(); err != nil {
		return meta.MQTTReadResult{}, err
	}
	r, err := p.options.Store.ReadMQTT(ctx, q)
	if canceled := ctx.Err(); canceled != nil {
		return meta.MQTTReadResult{}, canceled
	}
	if err != nil {
		return meta.MQTTReadResult{}, err
	}
	if r.Session != nil || r.Membership != nil || r.Accounting != nil || len(r.Sessions)+len(r.Subscriptions)+len(r.DeliveryCursors)+len(r.Inflight)+len(r.SourceOwners)+len(r.Directory)+len(r.Wills) != 0 {
		return meta.MQTTReadResult{}, ErrEvidence
	}
	return r, nil
}

func (p *InboxAdmission) current(ctx context.Context, id string) (*meta.MQTTInboxAdmissionView, error) {
	r, err := p.read(ctx, meta.MQTTRead{Kind: meta.MQTTReadInboxAdmission, AdmissionChannel: id})
	if err != nil {
		return nil, err
	}
	if !r.Done || r.After != (meta.MQTTReadCursor{}) || len(r.Bindings) != 0 || meta.ValidateMQTTInboxAdmissionView(id, r.Admission) != nil {
		return nil, ErrEvidence
	}
	return r.Admission, nil
}

// inboxBindingKeyAfter compares a fixed owner's native length-prefixed tuple.
func inboxBindingKeyAfter(a, b meta.MQTTSourceBindingKey) bool {
	if b == (meta.MQTTSourceBindingKey{}) {
		return false
	}
	if a == (meta.MQTTSourceBindingKey{}) {
		return true
	}
	if a.Owner != b.Owner {
		return false
	}
	for _, pair := range [][2]string{{a.Namespace, b.Namespace}, {a.ClientID, b.ClientID}} {
		if len(pair[0]) != len(pair[1]) {
			return len(pair[0]) < len(pair[1])
		}
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	if a.SessionGeneration != b.SessionGeneration {
		return a.SessionGeneration < b.SessionGeneration
	}
	return a.SubscriptionGeneration < b.SubscriptionGeneration
}

func validateInboxAdmissionCandidates(q meta.MQTTRead, r meta.MQTTReadResult) error {
	if r.Admission != nil || len(r.Bindings) > q.Limit || !r.Done && len(r.Bindings) != q.Limit {
		return ErrEvidence
	}
	previous := q.After.Binding
	for _, row := range r.Bindings {
		if meta.ValidateMQTTSourceBinding(row) != nil || row.Key.Owner != q.Owner || row.UID != q.Owner.ID || row.AuthorizationVersion != 0 || row.Stage > meta.MQTTBindingActive || !inboxBindingKeyAfter(previous, row.Key) {
			return ErrEvidence
		}
		previous = row.Key
	}
	// Candidate read kind 11 retains the input cursor on terminal pages.
	want := q.After
	if !r.Done {
		want.Binding = previous
	}
	if r.After != want {
		return ErrEvidence
	}
	return nil
}

func validInboxAdmissionPreparation(q meta.MQTTSourceBinding, ch SourceChannel, r PreparedInboxSource) bool {
	if !r.Needed {
		return r == (PreparedInboxSource{})
	}
	// Source preparation rechecks current intent. An established binding keeps
	// its original intent revision when the subscription later becomes Active.
	b, c := r.Binding, r.Cursor
	if meta.ValidateMQTTSourceBinding(b) != nil || meta.ValidateMQTTDeliveryCursor(c) != nil || b.Stage != meta.MQTTBindingActive || !b.BoundaryKnown || b.AuthorizationVersion != 0 || b.UID != q.UID || b.Topic != q.Topic || b.OperationID != q.OperationID {
		return false
	}
	k := q.Key
	k.Owner = b.Key.Owner
	if k != b.Key || k.Owner.Kind != meta.MQTTBindingChannel || k.Owner.ID != "1:"+ch.ID {
		return false
	}
	cursorKey := meta.MQTTDeliveryCursorKey{Namespace: k.Namespace, ClientID: k.ClientID, SessionGeneration: k.SessionGeneration, SubscriptionGeneration: k.SubscriptionGeneration, SourceKind: meta.MQTTSourceChannel, SourceID: k.Owner.ID, SourceGeneration: k.Owner.Generation}
	return c.Key == cursorKey && c.StartAfter == b.StartAfter && c.CompletedThrough >= b.CompletedThrough && c.Revision >= b.ProgressRevision
}
