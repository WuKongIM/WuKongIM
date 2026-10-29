package presence

import (
	"context"
	"sync"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

// RecoveryDirectory owns reconstructed UID proofs under exact authority fences.
type RecoveryDirectory interface {
	RecoveryUIDs(RouteTarget, []string) ([]string, error)
	InstallRecoveredRoutes(RouteTarget, []string, []Route) error
	EndpointsByUIDs(RouteTarget, []string) ([]Route, error)
}

// RecoveryOwners returns a complete bounded read from all current gateway owners.
// Partial/unavailable owner evidence must be returned as an error.
type RecoveryOwners interface {
	RecoverRoutes(context.Context, RouteTarget, []string) ([]Route, error)
}

// RecoveryOptions controls optional diagnostic accounting without changing
// proof, deadlines or publication behavior.
type RecoveryOptions struct {
	// ResultCensus scans decoded result/page structure. Enable only with the
	// existing opt-in RPC diagnostics; cheap scheduler counts are always retained.
	ResultCensus bool
}

// NewAuthorityRecoveryWithOptions fixes diagnostic accounting at construction.
func NewAuthorityRecoveryWithOptions(directory RecoveryDirectory, owners RecoveryOwners, options RecoveryOptions) *AuthorityRecovery {
	// A typed-nil built-in Directory is missing support, not a usable port.
	if builtin, ok := directory.(*authority.Directory); ok && builtin == nil {
		directory = nil
	}
	r := &AuthorityRecovery{directory: directory, owners: owners, resultCensus: options.ResultCensus,
		inFlight: make(map[recoveryUIDKey]*recoveryUIDFlight), members: make(map[*recoveryMember]struct{}),
		active: make(map[*recoveryExecution]struct{}), drained: make(chan struct{})}
	if bounded, ok := owners.(RecoveryCallBatchOwnersBounded); ok && bounded.SupportsBoundedRecoveryResults() {
		r.boundedOwners = bounded
		r.resultBudget = NewRecoveryResultBudget(RecoveryResultMaxRows, RecoveryResultMaxBytes)
		if shared, ok := owners.(interface{ RecoveryResultBudget() *RecoveryResultBudget }); ok {
			if budget := shared.RecoveryResultBudget(); budget != nil {
				r.resultBudget = budget
			}
		}
	}
	if slots, ok := owners.(*SlotRecovery); ok && directory == slots {
		slots.admission = r
		r.fullSlots = slots
	}
	return r
}

// AuthorityRecovery reconstructs requested UIDs with bounded owner queries.
// Bounded UID flights coalesce overlapping proofs without per-UID goroutines.
// Directory ownership fences every completion.
type AuthorityRecovery struct {
	// fullSlots enables page-sized physical turns while preserving UID admission.
	fullSlots *SlotRecovery
	directory RecoveryDirectory
	owners    RecoveryOwners
	// boundedOwners is fixed at construction only after all provider paths opt in.
	boundedOwners RecoveryCallBatchOwnersBounded
	resultBudget  *RecoveryResultBudget
	// resultCensus is fixed before admission; disabled operation never scans
	// decoded pages/results or selects the observed provider capability.
	resultCensus bool
	// mu protects the joint FIFO, identity and ownership budgets; never owner I/O.
	mu sync.Mutex
	// inFlight owns fenced UID clones until their provider and installation join.
	inFlight map[recoveryUIDKey]*recoveryUIDFlight
	// ownedUIDBytes covers UID clones and conservative caller/index metadata.
	ownedUIDBytes int
	// members charges waiting and active callers together, including canceled
	// creators whose actual provider still owns their work.
	members map[*recoveryMember]struct{}
	// queue is FIFO and contains only accepted, unfinished work.
	queue []*recoveryWork
	// active bounds actual batches independently of original-call result credits.
	active map[*recoveryExecution]struct{}
	// resultCalls counts original calls through provider completion and install.
	// Legacy ports cap this at 16; bounded ports separately charge actual results.
	resultCalls int
	// census stores fixed aggregate dispatch/result observations under mu.
	census RecoveryStats
	// dispatchPending gives ready admissions/completions one temporary dispatcher.
	dispatchPending bool
	// closed fences admission; drained closes only after callers and real work join.
	closed      bool
	drained     chan struct{}
	drainClosed bool
}

// NewAuthorityRecovery creates bounded UID proof admission for owner reconstruction.
func NewAuthorityRecovery(directory RecoveryDirectory, owners RecoveryOwners) *AuthorityRecovery {
	return NewAuthorityRecoveryWithOptions(directory, owners, RecoveryOptions{})
}

// RecoveryBatchOwners returns aligned proofs while sharing each owner read
// across every target in one bounded recipient page.
type RecoveryBatchOwners interface {
	RecoverRoutesByTargets(context.Context, []EndpointLookupGroup) []EndpointLookupResult
}

// Endpoints resolves a single target through the same bounded batch path.
func (r *AuthorityRecovery) Endpoints(ctx context.Context, target RouteTarget, uids []string) ([]Route, error) {
	result := r.EndpointsByTargets(ctx, []EndpointLookupGroup{{Target: target, UIDs: uids}})[0]
	return result.Routes, result.Err
}

// EndpointsByTargets packs at most 512 UIDs and 256 targets per owner round.
// Newly owned proofs share one bounded owner round; matching UIDs coalesce.
// Proven lookups stay outside recovery admission; failed groups remain aligned.
func (r *AuthorityRecovery) EndpointsByTargets(ctx context.Context, groups []EndpointLookupGroup) []EndpointLookupResult {
	results := make([]EndpointLookupResult, len(groups))
	page := make([]EndpointLookupGroup, 0, min(len(groups), 256))
	indexes := make([]int, 0, cap(page))
	count := 0
	flush := func() {
		got := r.recoverPage(ctx, page)
		for j, result := range got {
			i := indexes[j]
			if results[i].Err != nil {
				continue
			}
			if result.Err != nil {
				results[i] = result
			} else {
				results[i].Routes = append(results[i].Routes, result.Routes...)
			}
		}
		page = page[:0]
		indexes = indexes[:0]
		count = 0
	}
	for i, group := range groups {
		for start := 0; start < len(group.UIDs) || start == 0; {
			if count == authority.RecoveryBatchSize || len(page) == 256 {
				flush()
			}
			end := min(start+authority.RecoveryBatchSize-count, len(group.UIDs))
			page = append(page, EndpointLookupGroup{Target: group.Target, UIDs: group.UIDs[start:end]})
			indexes = append(indexes, i)
			count += end - start
			if end == len(group.UIDs) {
				break
			}
			start = end
		}
	}
	if len(page) != 0 {
		flush()
	}
	return results
}
