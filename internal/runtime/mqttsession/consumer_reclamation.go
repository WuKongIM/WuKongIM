package mqttsession

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ConsumerSessionReclamation rereads Session authority and proves any required
// exact-owner isolation before one bounded cleanup. Calls must join all effects.
// A true result confirms completion, including retries, not remote GC authority.
type ConsumerSessionReclamation interface {
	ReclaimSession(context.Context, meta.MQTTSessionCursor) (bool, error)
}

// ConsumerReclamationIndex resumes at most one durable historical backfill page.
// Coverage completion permits discovery; it never means cleanup is complete.
type ConsumerReclamationIndex interface {
	BuildMQTTReclamationIndex(context.Context, uint16) (meta.MQTTReclamationIndexResult, error)
}

func consumerReclamationCandidates(q meta.MQTTRead, r meta.MQTTReadResult) ([]meta.MQTTReadCursor, error) {
	if q.Kind != meta.MQTTReadSessionReclamation || meta.ValidateMQTTRead(q) != nil || r.Session != nil || r.Runtime != nil || r.Admission != nil || r.Membership != nil || r.Accounting != nil || len(r.Directory)+len(r.SourceOwners)+len(r.Subscriptions)+len(r.Bindings)+len(r.DeliveryCursors)+len(r.Inflight)+len(r.Wills) != 0 || len(r.Sessions) > q.Limit || len(r.Sessions) == 0 && !r.Done {
		return nil, ErrDeadlineScanEvidence
	}
	after := q.After
	out := make([]meta.MQTTReadCursor, 0, len(r.Sessions))
	for _, row := range r.Sessions {
		if meta.ValidateMQTTSession(row) != nil || meta.MQTTSessionReclamationTarget(row) == 0 {
			return nil, ErrDeadlineScanEvidence
		}
		next := meta.MQTTReadCursor{Session: meta.MQTTSessionCursor{Namespace: row.Namespace, ClientID: row.ClientID}}
		if meta.CompareMQTTSessionCursors(after.Session, next.Session) >= 0 {
			return nil, ErrDeadlineScanEvidence
		}
		out = append(out, next)
		after = next
	}
	if r.After != after {
		return nil, ErrDeadlineScanEvidence
	}
	return out, nil
}

// buildReclamationIndex converts a dependency panic or late reply into a failed
// page. The scanner retains no ready hint and can fairly revisit durable progress.
func (w *ConsumerWorker) buildReclamationIndex(ctx context.Context, slot uint16) (out meta.MQTTReclamationIndexResult, err error) {
	defer func() {
		if recover() != nil {
			err = ErrDeadlineScanEvidence
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if err != nil {
			out = meta.MQTTReclamationIndexResult{}
		}
	}()
	if err = ctx.Err(); err != nil {
		return out, err
	}
	out, err = w.opts.ReclamationIndex.BuildMQTTReclamationIndex(ctx, slot)
	if err == nil && (out.Scanned < 0 || out.Scanned > 64 || !out.Done && out.Scanned != 64) {
		err = ErrDeadlineScanEvidence
	}
	return out, err
}

// reclaimSession keeps failed callbacks inside the cohort's ordinary result
// path, so a panic cannot strand a body-free admission key until restart.
func (w *ConsumerWorker) reclaimSession(ctx context.Context, key meta.MQTTSessionCursor) (confirmed bool, err error) {
	defer func() {
		if recover() != nil {
			err = ErrDeadlineScanEvidence
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if err != nil {
			confirmed = false
		}
	}()
	if err = ctx.Err(); err != nil {
		return false, err
	}
	return w.opts.Reclamation.ReclaimSession(ctx, key)
}
