package mqttsession

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// completeEnqueued may rebase only the stream's already-enqueued private token.
// It never retries a payload or re-evaluates a previously captured original debit.
func (s *DeliveryStream) completeEnqueued(ctx context.Context, op *subscriptionOperation) error {
	c, w := s.pending, s.sender.window
	if c == nil || c.issuer != w || c.owner != s.connection.Owner || c.preclaimed {
		return ErrEvidence
	}
	m := c.mutation
	r, err := w.read(ctx, op, meta.MQTTRead{Kind: meta.MQTTReadAccounting, CursorKey: m.Key})
	if err != nil {
		return err
	}
	if err = w.guard.checkSession(ctx, op, c.owner, r.Session); err != nil {
		return err
	}
	if r.Session.Revision < m.ExpectedRevision || len(r.DeliveryCursors) != 1 {
		return ErrEvidence
	}
	cursor := r.DeliveryCursors[0]
	if cursor.Key != m.Key || cursor.AccountingVersion != 1 || cursor.Revision > r.Session.Revision || cursor.PendingMessages > r.Session.PendingMessages || cursor.PendingBytes > r.Session.PendingBytes || meta.ValidateMQTTAccountingHead(cursor, r.Accounting) != nil {
		return ErrEvidence
	}
	if err = w.check(ctx, op, c.owner, *r.Session, max(m.UpdatedAtMS, cursor.UpdatedAtMS)); err != nil {
		return err
	}
	if cursor.WindowThrough >= m.Through {
		s.pending = nil
		return nil
	}
	if cursor.WindowThrough+1 != m.Through || cursor.AccountedThrough < m.Through {
		return ErrConflict
	}
	var count, bytes uint64
	if head := r.Accounting; head != nil {
		if head.NextFrom != 0 && head.NextFrom <= m.Through {
			return ErrEvidence
		}
		for _, item := range head.Items {
			if item.Position > cursor.WindowThrough && item.Position <= m.Through {
				count++
				bytes += item.Bytes
			}
		}
	}
	if count != m.ReleasedMessages || bytes != m.ReleasedBytes {
		return ErrEvidence
	}
	now, err := w.guard.now()
	if err != nil {
		return err
	}
	if now.UnixMilli() < max(m.UpdatedAtMS, cursor.UpdatedAtMS, r.Session.UpdatedAtMS) {
		return ErrClock
	}
	m.ExpectedRevision, m.UpdatedAtMS = r.Session.Revision, now.UnixMilli()
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return err
	}
	if _, err = w.commit(ctx, op, m); err != nil {
		return err
	}
	if err = w.check(ctx, op, c.owner, *r.Session, m.UpdatedAtMS); err != nil {
		return err
	}
	s.pending = nil
	return nil
}

// authorizeEnqueue makes current receive authorization the last authoritative
// dependency before enqueue. Later revocation cannot retract this admitted send.
func (s *DeliveryStream) authorizeEnqueue(ctx context.Context, op *subscriptionOperation, d PreparedDelivery) error {
	w, p := s.sender.window, d.permission
	if p == nil || d.Owner != s.connection.Owner {
		return ErrEvidence
	}
	q := meta.MQTTRead{Kind: meta.MQTTReadInflight, Namespace: d.Owner.Key.Namespace, ClientID: d.Owner.Key.ClientID, SessionGeneration: d.Owner.SessionGeneration, PacketID: d.Exchange.PacketID}
	if d.QoS == 0 {
		q.Kind, q.PacketID, q.Topic = meta.MQTTReadSubscription, 0, d.Topic
	}
	r, err := w.read(ctx, op, q)
	if err != nil {
		return err
	}
	if err = w.guard.checkSession(ctx, op, d.Owner, r.Session); err != nil {
		return err
	}
	if d.QoS == 1 {
		if len(r.Inflight) != 1 {
			return ErrConflict
		}
		e := r.Inflight[0]
		if meta.ValidateMQTTInflight(e) != nil || e.Key != d.Exchange.Key || e.PacketID != d.Exchange.PacketID || e.DeliveryOrder != d.Exchange.DeliveryOrder || e.Publication != d.Exchange.Publication || e.Topic != d.Topic || e.UpdatedAtMS > r.Session.UpdatedAtMS || r.Session.OutboundInflight == 0 || r.Session.PendingBytes < e.Publication.Bytes {
			return ErrEvidence
		}
	} else {
		if d.QoS != 0 || d.completion == nil || d.completion.issuer != w || len(r.Subscriptions) != 1 {
			return ErrEvidence
		}
		sub := r.Subscriptions[0]
		if !validSubscriptionEvidence(sub, *r.Session) || sub.Stage != meta.MQTTSubscriptionActive || sub.Topic != d.Topic || sub.Generation != d.completion.mutation.Key.SubscriptionGeneration || sub.Revision != p.subscriptionRevision {
			return ErrConflict
		}
	}
	version, err := w.guard.authorize(ctx, op, p.request)
	if err != nil {
		return err
	}
	if version != p.version {
		return ErrSubscriptionRevoked
	}
	return checkSubscriptionScope(ctx, op)
}
