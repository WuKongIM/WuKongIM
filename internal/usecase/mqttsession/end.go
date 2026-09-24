package mqttsession

import (
	"context"
	"errors"
	"math"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ErrLifecycleCallback omits potentially sensitive dependency panic details.
var ErrLifecycleCallback = errors.New("mqttsession: lifecycle dependency failed")

// EndCommand carries a trusted application decision, never a client-supplied
// reason. The caller establishes definitive denial/loss and releases every owner
// operation before End; unavailable authority is not a revocation decision.
type EndCommand struct {
	Owner  contract.Owner
	Reason meta.MQTTSessionEndReason
}

// End isolates the exact observed owner, then ends its lifetime and resolves any
// live Will atomically. It preserves delivery/source responsibility for verified
// cleanup and does not follow a successor. Callers supply a bounded context.
func (a *App) End(ctx context.Context, c EndCommand) (err error) {
	if a == nil || ctx == nil || c.Owner.Validate() != nil ||
		(c.Reason != meta.MQTTSessionRevoked && c.Reason != meta.MQTTSessionSourceLost && c.Reason != meta.MQTTSessionQuota && c.Reason != meta.MQTTSessionExplicit) {
		return ErrInvalid
	}
	defer func() {
		if recover() != nil {
			err = ErrLifecycleCallback
		}
	}()
	if err = ctx.Err(); err != nil {
		return err
	}
	observed, err := a.now()
	if err != nil {
		return err
	}
	row, err := a.readEndingSession(ctx, c.Owner)
	if err != nil {
		return err
	}
	if observed.UnixMilli() < row.UpdatedAtMS {
		return ErrClock
	}
	uid := row.UID
	// Ended rows can result from quota accounting before socket isolation. Never
	// infer closure from a durable state, an expired lease or an absent registry.
	if err = a.opts.Isolation.Quiesce(ctx, c.Owner); err != nil {
		return err
	}
	row, err = a.readEndingSession(ctx, c.Owner)
	if err != nil {
		return err
	}
	if row.UID != uid {
		return ErrEvidence
	}
	now, err := a.now()
	if err != nil {
		return err
	}
	if now.Before(observed) || now.UnixMilli() < observed.UnixMilli() || now.UnixMilli() < row.UpdatedAtMS {
		return ErrClock
	}
	if row.State == meta.MQTTSessionActive && observed.UnixMilli() >= row.LeaseUntilMS {
		// Preserve the lost execution boundary before ending. A late cleanup must
		// not restart Will Delay at observation or isolation completion time.
		if row.LeaseUntilMS < row.UpdatedAtMS {
			return ErrClock
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = a.disconnectRow(ctx, row, row.LeaseUntilMS, false, row.SessionExpirySec); err != nil {
			return err
		}
		revision := row.Revision
		row, err = a.readEndingSession(ctx, c.Owner)
		if err != nil {
			return err
		}
		if row.UID != uid || row.Revision != revision+1 || row.State == meta.MQTTSessionActive {
			return ErrConflict
		}
		now, err = a.now()
		if err != nil {
			return err
		}
		if now.Before(observed) || now.UnixMilli() < observed.UnixMilli() || now.UnixMilli() < row.UpdatedAtMS {
			return ErrClock
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if row.State == meta.MQTTSessionEnded {
		return nil // Preserve the first committed reason and detached Will work.
	}
	next := row
	next.Revision++
	next.UpdatedAtMS = max(observed.UnixMilli(), row.UpdatedAtMS)
	next.State, next.TerminationReason = meta.MQTTSessionEnded, c.Reason
	next.LeaseUntilMS, next.OfflineExpiresAtMS = 0, 0
	r, err := a.commit(ctx, lifecycle(row, true, next, meta.MQTTLifecycleEnd))
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if r.WillGeneration != 0 {
		return ErrEvidence
	}
	return nil
}

// readEndingSession accepts only a complete current point read of the exact
// owner. It checks cancellation on both sides of a possibly remote dependency.
func (a *App) readEndingSession(ctx context.Context, o contract.Owner) (meta.MQTTSession, error) {
	if err := ctx.Err(); err != nil {
		return meta.MQTTSession{}, err
	}
	r, err := a.opts.Store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: o.Key.Namespace, ClientID: o.Key.ClientID})
	if err != nil {
		return meta.MQTTSession{}, err
	}
	if err = ctx.Err(); err != nil {
		return meta.MQTTSession{}, err
	}
	if !r.Done || r.After != (meta.MQTTReadCursor{}) || r.Accounting != nil || len(r.SourceOwners) != 0 || len(r.Sessions) != 0 || len(r.Subscriptions) != 0 || len(r.DeliveryCursors) != 0 || len(r.Inflight) != 0 || len(r.Bindings) != 0 || len(r.Wills) != 0 {
		return meta.MQTTSession{}, ErrEvidence
	}
	if r.Session == nil {
		return meta.MQTTSession{}, ErrFenced
	}
	s := *r.Session
	if meta.ValidateMQTTSession(s) != nil || s.State != meta.MQTTSessionEnded && s.Revision == math.MaxUint64 || s.State == meta.MQTTSessionEnded && s.WillGeneration != 0 {
		return meta.MQTTSession{}, ErrEvidence
	}
	if sessionOwner(s) != o {
		return meta.MQTTSession{}, ErrFenced
	}
	return s, nil
}
