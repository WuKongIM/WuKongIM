package mqttsession

import (
	"context"
	"errors"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// WillReclamationJournal lists a bounded body-free page and deletes captured
// local records. It supplies neither Slot authority nor non-dispatch proof.
type WillReclamationJournal interface {
	ReclamationCandidates(context.Context) ([]contract.WillAttempt, error)
	ReleaseAttempt(context.Context, contract.WillAttempt) error
}

// prepareAttempt yields after pressure cleanup even when capacity was released.
// The next ordinary execution turn must claim and authorize again before dispatch.
func (e *WillExecutor) prepareAttempt(ctx context.Context, a contract.WillAttempt) error {
	err := e.opts.DispatchFence.PrepareAttempt(ctx, a)
	if errors.Is(err, contract.ErrWillAttemptCapacity) && e.opts.ReclamationJournal != nil {
		return errors.Join(err, e.reclaimAttempts(ctx))
	}
	return err
}

// reclaimAttempts never uses absence, time or uncertain commits as deletion
// authority. Exact terminal or strictly newer execution rows fence old attempts.
func (e *WillExecutor) reclaimAttempts(parent context.Context) error {
	select {
	case e.reclamation <- struct{}{}:
		defer func() { <-e.reclamation }()
	default:
		return ErrWillBusy
	}
	ctx, cancel := context.WithTimeout(parent, 750*time.Millisecond)
	defer cancel()
	attempts, err := e.opts.ReclamationJournal.ReclamationCandidates(ctx)
	if err != nil {
		return err
	}
	if len(attempts) > contract.MaxWillAttemptReclamation {
		return ErrEvidence
	}
	seen := make(map[contract.WillAttempt]struct{}, len(attempts))
	for _, a := range attempts {
		if _, duplicate := seen[a]; duplicate || a.Validate() != nil || a.NodeID != e.opts.NodeID {
			return ErrEvidence
		}
		seen[a] = struct{}{}
	}
	var failures error
	for _, a := range attempts {
		if err := ctx.Err(); err != nil {
			return errors.Join(failures, err)
		}
		key := meta.MQTTWillKey{Namespace: a.Key.Namespace, ClientID: a.Key.ClientID, SessionGeneration: a.SessionGeneration, WillGeneration: a.WillGeneration}
		call, done := context.WithTimeout(ctx, 250*time.Millisecond)
		row, readErr := e.opts.Store.ReadMQTT(call, meta.MQTTRead{Kind: meta.MQTTReadWill, WillKey: key})
		if readErr == nil {
			readErr = call.Err()
		}
		done()
		if readErr != nil {
			failures = errors.Join(failures, readErr)
			continue
		}
		if len(row.Wills) == 0 {
			continue // A delayed unknown proposal may still install this attempt.
		}
		if len(row.Wills) != 1 || row.Wills[0].Key != key || meta.ValidateMQTTWill(row.Wills[0]) != nil {
			failures = errors.Join(failures, ErrEvidence)
			continue
		}
		w := row.Wills[0]
		retired := w.ExecutionGeneration > a.ExecutionGeneration
		if w.ExecutionGeneration == a.ExecutionGeneration && willAttempt(w) == a {
			retired = w.Stage == meta.MQTTWillPublished || w.Stage == meta.MQTTWillRejected
		}
		if retired {
			failures = errors.Join(failures, e.opts.ReclamationJournal.ReleaseAttempt(ctx, a))
		}
	}
	return errors.Join(failures, ctx.Err())
}
