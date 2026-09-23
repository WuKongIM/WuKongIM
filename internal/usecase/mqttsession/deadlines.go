package mqttsession

import (
	"context"
	"math"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ReconcileDeadline advances at most one due Session lifecycle transition.
// The observed owner is only a scan candidate: current authority, exact isolation
// for active owners, and a committed conditional result remain mandatory.
// Callers page both Session deadlines and Waiting Will recovery deadlines.
func (a *App) ReconcileDeadline(ctx context.Context, observed contract.Owner) error {
	if a == nil || ctx == nil || observed.Validate() != nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	row, found, err := a.read(ctx, observed.Key)
	if err != nil {
		return err
	}
	if !found || sessionOwner(row) != observed {
		return ErrFenced
	}
	if row.State == meta.MQTTSessionEnded {
		return nil
	}
	now, err := a.now()
	if err != nil {
		return err
	}
	if now.UnixMilli() < row.UpdatedAtMS {
		return ErrClock
	}
	if row.State == meta.MQTTSessionActive {
		if now.UnixMilli() < row.LeaseUntilMS {
			return nil
		}
		// Reuse the exact-owner drain and post-isolation reread. The recorded
		// execution boundary, rather than sweep latency, starts the offline clock.
		return a.Disconnect(ctx, DisconnectCommand{Owner: observed})
	}
	event := meta.MQTTLifecycleEnd
	if row.WillGeneration != 0 {
		// Session and referenced Will must belong to the same authoritative
		// snapshot. A concurrent reconnect cannot lend its Session to an old Will.
		var dueAt int64
		row, dueAt, err = a.waitingWill(ctx, row)
		if err != nil {
			return err
		}
		now, err = a.now()
		if err != nil {
			return err
		}
		if now.UnixMilli() < row.UpdatedAtMS {
			return ErrClock
		}
		if now.UnixMilli() < dueAt {
			return nil
		}
		event = meta.MQTTLifecycleWillDue
	} else if now.UnixMilli() < row.OfflineExpiresAtMS {
		return nil
	}
	if row.Revision == math.MaxUint64 {
		return ErrEvidence
	}
	next := row
	next.Revision++
	next.UpdatedAtMS = now.UnixMilli()
	if now.UnixMilli() >= row.OfflineExpiresAtMS {
		next.State, next.OfflineExpiresAtMS = meta.MQTTSessionEnded, 0
		next.TerminationReason = meta.MQTTSessionExpired
	}
	r, err := a.commit(ctx, lifecycle(row, true, next, event))
	if err != nil {
		return err
	}
	if r.WillGeneration != 0 {
		return ErrEvidence
	}
	return nil
}

// waitingWill validates the complete live reference and returns its coherent
// Session version. No detached or terminal obligation can be resurrected here.
func (a *App) waitingWill(ctx context.Context, candidate meta.MQTTSession) (meta.MQTTSession, int64, error) {
	key := meta.MQTTWillKey{Namespace: candidate.Namespace, ClientID: candidate.ClientID, SessionGeneration: candidate.Generation, WillGeneration: candidate.WillGeneration}
	r, err := a.opts.Store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadWill, WillKey: key})
	if err != nil {
		return meta.MQTTSession{}, 0, err
	}
	if r.Session == nil || meta.ValidateMQTTSession(*r.Session) != nil || len(r.Wills) != 1 || meta.ValidateMQTTWill(r.Wills[0]) != nil || r.Wills[0].Key != key {
		return meta.MQTTSession{}, 0, ErrEvidence
	}
	s, w := *r.Session, r.Wills[0]
	if sessionOwner(s) != sessionOwner(candidate) {
		return meta.MQTTSession{}, 0, ErrFenced
	}
	if s.State != meta.MQTTSessionOffline || s.WillGeneration != key.WillGeneration {
		return meta.MQTTSession{}, 0, ErrConflict
	}
	if w.Stage != meta.MQTTWillWaiting || w.UID != s.UID || w.OwnerGeneration != s.OwnerGeneration || w.OwnerNodeID != s.OwnerNodeID || w.OwnerBootID != s.OwnerBootID || w.ConnectionID != s.ConnectionID || w.DecisionRevision > s.Revision || w.UpdatedAtMS > s.UpdatedAtMS || w.DueAtMS > s.OfflineExpiresAtMS {
		return meta.MQTTSession{}, 0, ErrEvidence
	}
	return s, w.DueAtMS, nil
}
