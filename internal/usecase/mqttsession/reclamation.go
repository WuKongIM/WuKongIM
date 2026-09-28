package mqttsession

import (
	"context"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// SessionReclamationMetadata supplies fresh Slot authority and one bounded,
// result-bearing cleanup proposal. Implementations must join their effects.
type SessionReclamationMetadata interface {
	ReadMQTT(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
	ReclaimMQTTSession(context.Context, meta.MQTTSessionReclamation) (meta.MQTTSessionReclamationResult, error)
}

type SessionReclamationOptions struct {
	Store SessionReclamationMetadata
	// Ender proves exact-owner isolation even when the durable row is already Ended.
	Ender SessionEnder
	// Timeout bounds the whole turn; default and maximum are five seconds.
	Timeout time.Duration
	// Now must not regress during a turn or behind the authoritative Session.
	Now func() time.Time
}

// SessionReclamation discards obsolete Session children only. It never releases
// source obligations, detached Wills or shared contents, or isolates a successor.
type SessionReclamation struct{ options SessionReclamationOptions }

// SessionReclamationResult describes timely confirmed metadata progress, not
// isolation or remote GC authority. Completed includes already-committed retries.
type SessionReclamationResult struct{ Changed, Completed bool }

func NewSessionReclamation(o SessionReclamationOptions) (*SessionReclamation, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Store == nil || o.Ender == nil || o.Timeout <= 0 || o.Timeout > 5*time.Second {
		return nil, ErrInvalid
	}
	return &SessionReclamation{options: o}, nil
}

// Reconcile captures one ended-generation boundary from a body-free hint. A
// current Ended lifetime still needs exact-owner End; a replaced lifetime never
// closes its successor. Each turn proposes at most one page and does not retry
// ambiguous writes. Durable discovery retains unfinished work for a fresh turn.
func (p *SessionReclamation) Reconcile(parent context.Context, hint meta.MQTTSessionCursor) (out SessionReclamationResult, err error) {
	q := meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: hint.Namespace, ClientID: hint.ClientID}
	if p == nil || parent == nil || meta.ValidateMQTTRead(q) != nil {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, p.options.Timeout)
	defer cancel()
	defer func() {
		if recover() != nil {
			err = ErrLifecycleCallback
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if err != nil {
			out = SessionReclamationResult{}
		}
	}()
	if err = ctx.Err(); err != nil {
		return out, err
	}
	observed := p.options.Now()
	if observed.UnixMilli() <= 0 {
		return out, ErrClock
	}
	row, err := p.read(ctx, q)
	if err != nil || row == nil {
		return out, err
	}
	through := meta.MQTTSessionReclamationTarget(*row)
	if through == 0 {
		return out, nil
	}
	if observed.UnixMilli() < row.UpdatedAtMS {
		return out, ErrClock
	}
	if through == row.Generation {
		owner, uid := sessionOwner(*row), row.UID
		if err = ctx.Err(); err != nil {
			return out, err
		}
		// End preserves the original reason and detached Will decisions. Explicit is
		// the trusted cleanup trigger, never a replacement for the stored end reason.
		if err = p.options.Ender.End(ctx, EndCommand{Owner: owner, Reason: meta.MQTTSessionExplicit}); err != nil {
			return out, err
		}
		row, err = p.read(ctx, q)
		if err != nil {
			return out, err
		}
		if row == nil || sessionOwner(*row) != owner || row.UID != uid || row.State != meta.MQTTSessionEnded {
			return out, ErrConflict
		}
	}
	now := p.options.Now()
	if now.Before(observed) || now.UnixMilli() < observed.UnixMilli() || now.UnixMilli() < row.UpdatedAtMS {
		return out, ErrClock
	}
	m := meta.MQTTSessionReclamation{Namespace: hint.Namespace, ClientID: hint.ClientID, ExpectedRevision: row.Revision, ThroughGeneration: through, UpdatedAtMS: now.UnixMilli()}
	if meta.ValidateMQTTSessionReclamation(m) != nil {
		return out, ErrEvidence
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	r, err := p.options.Store.ReclaimMQTTSession(ctx, m)
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if r.RemovedSubscriptions < 0 || r.RemovedSubscriptions > 64 {
		return out, ErrEvidence
	}
	switch r.Status {
	case meta.MQTTSessionCASConflict:
		if r.Done || r.RemovedSubscriptions != 0 {
			return out, ErrEvidence
		}
		return out, ErrConflict
	case meta.MQTTSessionCASApplied:
		if r.CurrentRevision != row.Revision+1 || r.Done && r.ReclaimedThroughGeneration != through || !r.Done && (r.ReclaimedThroughGeneration != row.ReclaimedThroughGeneration || r.ReclaimedThroughGeneration >= through || r.RemovedSubscriptions != 64) {
			return out, ErrEvidence
		}
		return SessionReclamationResult{Changed: true, Completed: r.Done}, nil
	case meta.MQTTSessionCASUnchanged:
		if !r.Done || r.CurrentRevision < row.Revision || r.ReclaimedThroughGeneration < through || r.RemovedSubscriptions != 0 {
			return out, ErrEvidence
		}
		return SessionReclamationResult{Completed: true}, nil
	default:
		return out, ErrEvidence
	}
}

func (p *SessionReclamation) read(ctx context.Context, q meta.MQTTRead) (*meta.MQTTSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := p.options.Store.ReadMQTT(ctx, q)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if !r.Done || r.After != (meta.MQTTReadCursor{}) || r.Runtime != nil || r.Admission != nil || r.Membership != nil || r.Accounting != nil || len(r.Directory)+len(r.SourceOwners)+len(r.Sessions)+len(r.Subscriptions)+len(r.DeliveryCursors)+len(r.Inflight)+len(r.Bindings)+len(r.Wills) != 0 {
		return nil, ErrEvidence
	}
	if r.Session == nil {
		return nil, nil
	}
	row := *r.Session
	if meta.ValidateMQTTSession(row) != nil || row.Namespace != q.Namespace || row.ClientID != q.ClientID || row.Revision == math.MaxUint64 {
		return nil, ErrEvidence
	}
	return &row, nil
}
