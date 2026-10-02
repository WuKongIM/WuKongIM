package mqttsession

import (
	"context"
	"errors"
	"math"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// Renew keeps ownership and all delivery/Will fields unchanged. Unconfirmed
// writes never extend local execution; the existing local deadline still holds.
func (a *App) Renew(ctx context.Context, o contract.Owner) (Lease, error) {
	if a == nil || ctx == nil || o.Validate() != nil {
		return Lease{}, ErrInvalid
	}
	op, err := a.opts.Owners.Begin(ctx, o)
	if err != nil {
		return Lease{}, err
	}
	defer op.Done() // A dependency panic must not leak an admitted scope.
	lease, err := a.renewCommitted(op.Context(), o)
	op.Done() // Release before fencing: a takeover may already be joining us.
	if err != nil {
		if errors.Is(err, ErrFenced) || errors.Is(err, ErrClock) || errors.Is(err, ErrEvidence) {
			_ = a.opts.Owners.Fence(o)
		}
		return Lease{}, err
	}
	if err = a.opts.Owners.Renew(o, lease.Revision, lease.Until); err != nil {
		_ = a.opts.Owners.Fence(o)
		return Lease{}, err
	}
	return lease, nil
}

func (a *App) renewCommitted(ctx context.Context, o contract.Owner) (Lease, error) {
	row, found, err := a.read(ctx, o.Key)
	if err != nil {
		return Lease{}, err
	}
	if !found || sessionOwner(row) != o || row.State != meta.MQTTSessionActive {
		return Lease{}, ErrFenced
	}
	now, err := a.now()
	if err != nil {
		return Lease{}, err
	}
	if now.UnixMilli() < row.UpdatedAtMS || now.UnixMilli() >= row.LeaseUntilMS || row.Revision == math.MaxUint64 {
		return Lease{}, ErrClock
	}
	next := row
	next.Revision++
	next.UpdatedAtMS = now.UnixMilli()
	until := now.Add(a.opts.LeaseDuration)
	next.LeaseUntilMS = leaseUpperBoundMS(until)
	r, err := a.opts.Store.CompareAndSwapMQTTSession(ctx, row.Revision, next)
	if err != nil {
		return Lease{}, err
	}
	if r.Status == meta.MQTTSessionCASConflict {
		return Lease{}, ErrConflict
	}
	if (r.Status != meta.MQTTSessionCASApplied && r.Status != meta.MQTTSessionCASUnchanged) || r.CurrentRevision != next.Revision {
		return Lease{}, ErrEvidence
	}
	return Lease{Revision: r.CurrentRevision, Until: until}, nil
}
