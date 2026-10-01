package mqttsession

import (
	"context"
	"errors"
	"strings"
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
	// Temporary observers describe actual bounded pages and read decisions,
	// never synthesize authority or change the journal's retirement policy.
	probe := ""
	// gofail: var wkMQTTWillReclamationProbeMatch string
	// probe = wkMQTTWillReclamationProbeMatch
	if probe != "" {
		for _, a := range attempts {
			if a.Key.ClientID == probe {
				// gofail: var wkMQTTWillReclamationProbeSeen bool
				// _ = wkMQTTWillReclamationProbeSeen
			}
		}
		// This observer follows the complete membership probe, so a later
		// selector update can exclude one already-loaded in-flight probe.
		// gofail: var wkMQTTWillReclamationProbePage bool
		// _ = wkMQTTWillReclamationProbePage
	}
	readMatch, readExempt := "", ""
	// gofail: var wkMQTTWillReclamationReadMatch string
	// readMatch = wkMQTTWillReclamationReadMatch

	// gofail: var wkMQTTWillReclamationReadExempt string
	// readExempt = wkMQTTWillReclamationReadExempt

	// gofail: var wkMQTTWillReclamationPage bool
	// _ = wkMQTTWillReclamationPage
	var failures error
	for _, a := range attempts {
		if err := ctx.Err(); err != nil {
			return errors.Join(failures, err)
		}
		key := meta.MQTTWillKey{Namespace: a.Key.Namespace, ClientID: a.Key.ClientID, SessionGeneration: a.SessionGeneration, WillGeneration: a.WillGeneration}
		call, done := context.WithTimeout(ctx, 250*time.Millisecond)
		if readMatch != "" && strings.HasPrefix(a.Key.ClientID, readMatch) && a.Key.ClientID != readExempt {
			// Exercise the actual foreground read with its existing child
			// deadline/cancellation; never manufacture an authority response.
			// gofail: var wkMQTTWillReclamationReadDeadline bool
			// if wkMQTTWillReclamationReadDeadline {
			//     <-call.Done()
			// }

			// gofail: var wkMQTTWillReclamationReadCancel bool
			// if wkMQTTWillReclamationReadCancel {
			//     done()
			// }
		}
		row, readErr := e.opts.Store.ReadMQTT(call, meta.MQTTRead{Kind: meta.MQTTReadWill, WillKey: key})
		// Observe only the Store's actual returned error, before the executor's
		// independent late-response guard adds a child-context error.
		if errors.Is(readErr, context.DeadlineExceeded) {
			// gofail: var wkMQTTWillReclamationDeadlineObserved bool
			// _ = wkMQTTWillReclamationDeadlineObserved
		}
		if errors.Is(readErr, context.Canceled) {
			// gofail: var wkMQTTWillReclamationCancelObserved bool
			// _ = wkMQTTWillReclamationCancelObserved
		}
		if readErr == nil {
			readErr = call.Err()
		}
		done()
		if readErr != nil {
			if a.ExecutionGeneration == 1 {
				// gofail: var wkMQTTWillReclamationFirstUnconfirmed bool
				// _ = wkMQTTWillReclamationFirstUnconfirmed
			}
			if a.ExecutionGeneration == 2 {
				// gofail: var wkMQTTWillReclamationSecondUnconfirmed bool
				// _ = wkMQTTWillReclamationSecondUnconfirmed
			}
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
		if !retired {
			if a.ExecutionGeneration == 1 {
				// gofail: var wkMQTTWillReclamationFirstRetained bool
				// _ = wkMQTTWillReclamationFirstRetained
			}
			if a.ExecutionGeneration == 2 {
				// gofail: var wkMQTTWillReclamationSecondRetained bool
				// _ = wkMQTTWillReclamationSecondRetained
			}
		}
		if retired {
			// gofail: var wkMQTTWillReclamationBeforeRelease bool
			// if wkMQTTWillReclamationBeforeRelease {
			//     return ErrEvidence
			// }
			releaseErr := e.opts.ReclamationJournal.ReleaseAttempt(ctx, a)
			if releaseErr == nil && readExempt != "" && a.Key.ClientID == readExempt {
				// gofail: var wkMQTTWillReclamationSelectedRetired bool
				// _ = wkMQTTWillReclamationSelectedRetired
			}
			failures = errors.Join(failures, releaseErr)
		}
	}
	return errors.Join(failures, ctx.Err())
}
