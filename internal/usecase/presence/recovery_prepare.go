package presence

import (
	"context"
	"sync"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

// Preparation reports one finite attempt, not a serving permission or a global
// readiness certificate. Pending includes failed and not-yet-attempted targets.
type Preparation struct {
	Attempted, Prepared, Pending int
}

// recoveryPreparation owns a control caller's terminal status without a UID,
// route payload or a second execution queue. done closes after actual work ends.
type recoveryPreparation struct {
	done chan struct{}
	err  error
}

// Prepare attempts at most 256 exact local-authority targets once. Its joined
// callers submit to the ordinary FIFO and share all physical/result budgets;
// they are not additional provider workers. No fake UID or retry is introduced.
// Context cancellation detaches callers; Close still owns late actual providers.
func (r *AuthorityRecovery) Prepare(ctx context.Context, targets []RouteTarget) (Preparation, error) {
	report := Preparation{Pending: len(targets)}
	if r == nil || r.fullSlots == nil || len(targets) > 256 {
		return report, authority.ErrRouteNotReady
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	next := 0
	for range min(len(targets), recoveryMaxCohorts) {
		wg.Add(1)
		goroutine.SafeGo(nil, goroutine.TaskPresenceRecoveryScheduler, func() {
			defer wg.Done()
			for {
				mu.Lock()
				if next == len(targets) || ctx.Err() != nil {
					mu.Unlock()
					return
				}
				target := targets[next]
				next++
				report.Attempted++
				mu.Unlock()
				err := r.prepareTarget(ctx, target)
				mu.Lock()
				if err == nil {
					report.Prepared++
					report.Pending--
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return report, ctx.Err()
}

func (r *AuthorityRecovery) prepareTarget(ctx context.Context, target RouteTarget) error {
	if _, err := r.fullSlots.current(target); err == nil {
		return ctx.Err()
	}
	r.mu.Lock()
	// One control target consumes one key allowance as well as caller/metadata
	// credits. It never appears in the UID index or creates an offline UID proof.
	// The caller charge also retains finished work/preparation status and all
	// three channels after active work credits return but before the caller does.
	const memberBytes, workBytes = 1024, 512
	if r.closed || ctx.Err() != nil || len(r.members) >= recoveryMaxMembers || len(r.inFlight)+r.preparationKeys >= recoveryMaxKeys || memberBytes+workBytes > recoveryMaxUIDBytes-r.ownedUIDBytes {
		r.mu.Unlock()
		return authority.ErrRouteNotReady
	}
	p := &recoveryPreparation{done: make(chan struct{})}
	m := &recoveryMember{ctx: ctx, stop: make(chan struct{}), bytes: memberBytes}
	w := &recoveryWork{creator: m, bytes: workBytes, started: make(chan struct{}), preparation: p,
		groups: []EndpointLookupGroup{{Target: target}}}
	w.slotCall = newSlotRecoveryCall(w.groups)
	m.work = w
	r.members[m] = struct{}{}
	r.ownedUIDBytes += memberBytes + workBytes
	r.preparationKeys++
	r.queue = append(r.queue, w)
	r.kickLocked()
	r.mu.Unlock()
	defer r.returnMember(m)
	select {
	case <-p.done:
		if err := ctx.Err(); err != nil {
			return err
		}
		return p.err
	case <-ctx.Done():
		return ctx.Err()
	case <-m.stop:
		return authority.ErrRouteNotReady
	}
}
