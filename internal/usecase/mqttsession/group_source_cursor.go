package mqttsession

import (
	"context"
	"math"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// initCursor preserves the first protected boundary and complete Preparing child.
// Only definite CAS rejection permits another bounded proposal under a newer
// parent; an independently read exact cursor can replace a redundant Init.
func (p *GroupSources) initCursor(ctx context.Context, op *preparationScope, o contract.Owner, sub meta.MQTTSubscription, key meta.MQTTDeliveryCursorKey, startAfter uint64) (meta.MQTTDeliveryCursor, error) {
	var previous uint64
	for attempt := 0; ; attempt++ {
		session, err := p.current(ctx, op, o, sub)
		if err != nil {
			return meta.MQTTDeliveryCursor{}, err
		}
		if attempt > 0 {
			if session.Revision <= previous {
				return meta.MQTTDeliveryCursor{}, ErrConflict
			}
			parent, cursor, found, err := p.readCursor(ctx, op, o, sub, key)
			if err != nil {
				return meta.MQTTDeliveryCursor{}, err
			}
			if parent.Revision < session.Revision {
				return meta.MQTTDeliveryCursor{}, ErrEvidence
			}
			if found {
				if cursor.StartAfter != startAfter || cursor.Revision <= previous {
					return meta.MQTTDeliveryCursor{}, ErrEvidence
				}
				return cursor, nil
			}
			if err = p.authorize(ctx, op, sub); err != nil {
				return meta.MQTTDeliveryCursor{}, err
			}
		}
		if session.Revision == math.MaxUint64 {
			return meta.MQTTDeliveryCursor{}, ErrEvidence
		}
		now, err := p.guard.now()
		if err != nil {
			return meta.MQTTDeliveryCursor{}, err
		}
		mutation := meta.MQTTDeliveryCursorMutation{Key: key, ExpectedRevision: session.Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTCursorInit, Topic: sub.Topic, AuthorizationVersion: sub.AuthorizationVersion, Through: startAfter, UpdatedAtMS: now.UnixMilli()}
		if err = op.check(ctx); err != nil {
			return meta.MQTTDeliveryCursor{}, err
		}
		receipt, err := p.options.Store.MutateMQTTDeliveryCursor(ctx, mutation)
		if err != nil {
			return meta.MQTTDeliveryCursor{}, err
		}
		if err = op.check(ctx); err != nil {
			return meta.MQTTDeliveryCursor{}, err
		}
		if receipt.Status == meta.MQTTSessionCASConflict {
			if attempt == 2 {
				return meta.MQTTDeliveryCursor{}, ErrConflict
			}
			previous = session.Revision
			continue
		}
		if (receipt.Status != meta.MQTTSessionCASApplied && receipt.Status != meta.MQTTSessionCASUnchanged) || receipt.CurrentRevision != session.Revision+1 || receipt.SessionState != session.State || receipt.TerminationReason != 0 {
			return meta.MQTTDeliveryCursor{}, ErrEvidence
		}
		_, cursor, found, err := p.readCursor(ctx, op, o, sub, key)
		if err != nil {
			return meta.MQTTDeliveryCursor{}, err
		}
		if !found || cursor.StartAfter != startAfter || cursor.Revision < receipt.CurrentRevision {
			return meta.MQTTDeliveryCursor{}, ErrEvidence
		}
		return cursor, nil
	}
}
