package mqttsession

import (
	"context"
	"math"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func (a *App) now() (time.Time, error) {
	t := a.opts.Now()
	if t == t.Round(0) || t.UnixMilli() <= 0 || t.UnixMilli() > math.MaxInt64-int64(math.MaxUint32)*1000-int64(time.Minute/time.Millisecond) {
		return time.Time{}, ErrClock
	}
	return t, nil
}

// leaseUpperBoundMS rounds only durable wall-clock representation upward.
// Local admission retains the original monotonic deadline and never extends it.
func leaseUpperBoundMS(t time.Time) int64 {
	ms := t.UnixMilli()
	if t.Nanosecond()%int(time.Millisecond) != 0 {
		ms++
	}
	return ms
}

func sessionOwner(s meta.MQTTSession) contract.Owner {
	return contract.Owner{Key: contract.Key{Namespace: s.Namespace, ClientID: s.ClientID}, SessionGeneration: s.Generation, OwnerGeneration: s.OwnerGeneration, NodeID: s.OwnerNodeID, BootID: s.OwnerBootID, ConnectionID: s.ConnectionID}
}
func (a *App) read(ctx context.Context, key contract.Key) (meta.MQTTSession, bool, error) {
	r, err := a.opts.Store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: key.Namespace, ClientID: key.ClientID})
	if err != nil {
		return meta.MQTTSession{}, false, err
	}
	if r.Session == nil {
		return meta.MQTTSession{}, false, nil
	}
	if meta.ValidateMQTTSession(*r.Session) != nil || r.Session.Namespace != key.Namespace || r.Session.ClientID != key.ClientID {
		return meta.MQTTSession{}, false, ErrEvidence
	}
	return *r.Session, true, nil
}
func lifecycle(old meta.MQTTSession, found bool, next meta.MQTTSession, event meta.MQTTLifecycleEvent) meta.MQTTLifecycleMutation {
	next.WillGeneration, next.LastLifecycleDigest = 0, ""
	m := meta.MQTTLifecycleMutation{Event: event, Session: next}
	if found {
		m.ExpectedRevision, m.ExpectedGeneration = old.Revision, old.Generation
		m.OwnerGeneration, m.OwnerNodeID, m.OwnerBootID, m.ConnectionID = old.OwnerGeneration, old.OwnerNodeID, old.OwnerBootID, old.ConnectionID
	}
	return m
}
func (a *App) commit(ctx context.Context, m meta.MQTTLifecycleMutation) (meta.MQTTLifecycleResult, error) {
	if err := meta.ValidateMQTTLifecycleMutation(m); err != nil {
		return meta.MQTTLifecycleResult{}, ErrInvalid
	}
	r, err := a.opts.Store.ApplyMQTTLifecycle(ctx, m)
	if err != nil {
		return meta.MQTTLifecycleResult{}, err
	}
	if r.Status == meta.MQTTSessionCASConflict {
		return r, ErrConflict
	}
	if (r.Status != meta.MQTTSessionCASApplied && r.Status != meta.MQTTSessionCASUnchanged) || r.CurrentRevision != m.Session.Revision || r.WillGeneration > r.CurrentRevision {
		return r, ErrEvidence
	}
	if m.Will != nil {
		if r.WillGeneration != m.Will.Key.WillGeneration {
			return r, ErrEvidence
		}
	} else if (m.Event == meta.MQTTLifecycleConnect || m.Event == meta.MQTTLifecycleNormalDisconnect) && r.WillGeneration != 0 {
		return r, ErrEvidence
	}
	return r, nil
}

// abandon fences independently of caller cancellation. Failed physical cleanup
// remains retained by Owners for its bounded sweep; no asynchronous work escapes.
func (a *App) abandon(o contract.Owner) error {
	if err := a.opts.Owners.Fence(o); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.opts.CleanupTimeout)
	defer cancel()
	return a.opts.Owners.Quiesce(ctx, o)
}

// Disconnect requires exact quiescence before recording the Session/Will result.
// Callers release their own operation scope before entering this joined path.
func (a *App) Disconnect(ctx context.Context, c DisconnectCommand) error {
	if a == nil || ctx == nil || c.Owner.Validate() != nil {
		return ErrInvalid
	}
	// Record the accepted disconnect before metadata/RPC/drain delays. Those
	// delays cannot change normal intent into expiry or restart its offline clock.
	observed, err := a.now()
	if err != nil {
		return err
	}
	if !c.ObservedAt.IsZero() {
		if c.ObservedAt == c.ObservedAt.Round(0) || c.ObservedAt.After(observed) || c.ObservedAt.UnixMilli() <= 0 {
			return ErrClock
		}
		observed = c.ObservedAt
	}
	row, found, err := a.read(ctx, c.Owner.Key)
	if err != nil {
		return err
	}
	if !found || sessionOwner(row) != c.Owner {
		return ErrFenced
	}
	if c.SessionExpirySec != nil && row.SessionExpirySec == 0 && *c.SessionExpirySec != 0 {
		return ErrInvalid
	}
	if err = a.opts.Isolation.Quiesce(ctx, c.Owner); err != nil {
		return err
	}
	row, found, err = a.read(ctx, c.Owner.Key)
	if err != nil {
		return err
	}
	if !found || sessionOwner(row) != c.Owner {
		return ErrFenced
	}
	if row.State != meta.MQTTSessionActive {
		return nil
	}
	now, err := a.now()
	if err != nil {
		return err
	}
	if now.Before(observed) || now.UnixMilli() < observed.UnixMilli() || now.UnixMilli() < row.UpdatedAtMS {
		return ErrClock
	}
	// A concurrently admitted mutation can advance UpdatedAt while isolation
	// drains it. Preserve that committed order without using completion latency.
	at, normal, expiry := max(observed.UnixMilli(), row.UpdatedAtMS), c.Normal, row.SessionExpirySec
	if observed.UnixMilli() >= row.LeaseUntilMS {
		if row.LeaseUntilMS < row.UpdatedAtMS {
			return ErrClock
		}
		at, normal = row.LeaseUntilMS, false
	} else if c.SessionExpirySec != nil {
		if row.SessionExpirySec == 0 && *c.SessionExpirySec != 0 {
			return ErrInvalid
		}
		expiry = min(*c.SessionExpirySec, a.opts.SessionExpiryLimitSec)
	}
	return a.disconnectRow(ctx, row, at, normal, expiry)
}

func (a *App) disconnectRow(ctx context.Context, row meta.MQTTSession, at int64, normal bool, expiry uint32) error {
	if row.Revision == math.MaxUint64 || at < row.UpdatedAtMS || at <= 0 || at > math.MaxInt64-int64(expiry)*1000 {
		return ErrClock
	}
	next := row
	next.Revision++
	next.UpdatedAtMS = at
	next.SessionExpirySec = expiry
	next.LeaseUntilMS = 0
	next.State, next.OfflineExpiresAtMS, next.TerminationReason = meta.MQTTSessionOffline, at+int64(expiry)*1000, 0
	if expiry == 0 {
		next.State, next.OfflineExpiresAtMS, next.TerminationReason = meta.MQTTSessionEnded, 0, meta.MQTTSessionExpired
	}
	event := meta.MQTTLifecycleDisconnectWithWill
	if normal {
		event = meta.MQTTLifecycleNormalDisconnect
	}
	_, err := a.commit(ctx, lifecycle(row, true, next, event))
	return err
}
