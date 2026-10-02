package mqttsession

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// readPendingSubscription checks one authoritative Session/child response before
// establishment or removal uses a recovery-index key as current intent.
func readPendingSubscription(ctx context.Context, store preparationReader, q meta.MQTTRead) (session meta.MQTTSession, sub meta.MQTTSubscription, found bool, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	r, err := store.ReadMQTT(ctx, q)
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
