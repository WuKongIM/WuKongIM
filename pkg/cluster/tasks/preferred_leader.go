package tasks

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	defaultPreferredLeaderCooldown       = 5 * time.Second
	defaultPreferredLeaderAttemptTimeout = 250 * time.Millisecond
	maxPreferredLeaderChecksPerReconcile = 4
)

const (
	preferredLeaderDecisionActiveTask    = "active_task"
	preferredLeaderDecisionCooldown      = "cooldown"
	preferredLeaderDecisionStaleIntent   = "stale_intent"
	preferredLeaderDecisionVoterMismatch = "voter_mismatch"
	preferredLeaderDecisionMatch         = "match"
	preferredLeaderDecisionTimeout       = "timeout"
	preferredLeaderDecisionError         = "error"
)

// PreferredLeaderRuntime is the strict Slot Raft surface needed to converge
// steady-state leadership placement without bypassing Raft eligibility.
type PreferredLeaderRuntime interface {
	Status(multiraft.SlotID) (multiraft.Status, error)
	TryTransferLeadershipToPreferred(
		context.Context,
		multiraft.SlotID,
		multiraft.NodeID,
		uint64,
		[]multiraft.NodeID,
		multiraft.NodeID,
		multiraft.PreferredLeaderTransferGuard,
	) (multiraft.PreferredLeaderTransferDecision, error)
}

// PreferredLeaderIntent identifies the exact applied Controller intent that
// authorizes one strict preferred-leader transfer enqueue.
type PreferredLeaderIntent struct {
	// Revision is the exact Controller snapshot revision used for the candidate.
	Revision uint64
	// SlotID identifies the physical Slot whose leadership placement is checked.
	SlotID uint32
	// ConfigEpoch fences the desired voter assignment.
	ConfigEpoch uint64
	// PreferredLeader is the exact Controller soft placement target.
	PreferredLeader uint64
	// DesiredPeers is the exact ordered peer assignment from the snapshot.
	DesiredPeers []uint64
}

// PreferredLeaderObserver receives bounded, low-cardinality reconciliation
// decisions and strict wait durations.
type PreferredLeaderObserver interface {
	ObservePreferredLeaderDecision(decision string)
	ObservePreferredLeaderStrictWait(decision string, d time.Duration)
}

// PreferredLeaderReconcilerConfig wires steady-state preferred-leader convergence.
type PreferredLeaderReconcilerConfig struct {
	// LocalNode is this node's stable cluster identity.
	LocalNode uint64
	// Runtime observes Slot state and performs a fresh, strictly fenced transfer.
	Runtime PreferredLeaderRuntime
	// Cooldown bounds repeated attempts for the same leader, term, target, and assignment epoch.
	Cooldown time.Duration
	// AttemptTimeout bounds one strict Raft-worker safety check. Zero defaults to 250ms.
	AttemptTimeout time.Duration
	// IntentGuard rechecks the exact latest applied Controller intent immediately
	// before enqueue and returns its generation context. The owner must cancel
	// that context before a newer snapshot apply begins.
	IntentGuard func(PreferredLeaderIntent) (multiraft.PreferredLeaderTransferGuard, bool)
	// Observer receives low-cardinality decisions and strict-check wait durations.
	Observer PreferredLeaderObserver
	// Now supplies time for cooldown accounting. Production callers leave it nil.
	Now func() time.Time
}

// PreferredLeaderReconciler asks the current Raft leader to converge to the
// Controller's preferred placement only after ordinary Controller tasks are idle.
type PreferredLeaderReconciler struct {
	cfg PreferredLeaderReconcilerConfig

	reconcileMu sync.Mutex
	mu          sync.Mutex
	attempts    map[uint32]preferredLeaderAttempt
	cursor      int
}

type preferredLeaderAttempt struct {
	fingerprint preferredLeaderFingerprint
	at          time.Time
}

type preferredLeaderFingerprint struct {
	leader      uint64
	term        uint64
	preferred   uint64
	configEpoch uint64
}

// NewPreferredLeaderReconciler creates a low-frequency, deduplicated placement reconciler.
func NewPreferredLeaderReconciler(cfg PreferredLeaderReconcilerConfig) *PreferredLeaderReconciler {
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = defaultPreferredLeaderCooldown
	}
	if cfg.AttemptTimeout <= 0 {
		cfg.AttemptTimeout = defaultPreferredLeaderAttemptTimeout
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &PreferredLeaderReconciler{
		cfg:      cfg,
		attempts: make(map[uint32]preferredLeaderAttempt),
	}
}

// Reconcile performs bounded strict checks and issues at most one real transfer per pass.
func (r *PreferredLeaderReconciler) Reconcile(ctx context.Context, snapshot control.Snapshot) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.cfg.LocalNode == 0 || r.cfg.Runtime == nil {
		return nil
	}
	r.reconcileMu.Lock()
	defer r.reconcileMu.Unlock()
	r.pruneAttempts(snapshot.Slots)
	if len(snapshot.Tasks) != 0 {
		r.observeDecision(preferredLeaderDecisionActiveTask)
		return nil
	}
	if len(snapshot.Slots) == 0 {
		return nil
	}

	start := r.cursor % len(snapshot.Slots)
	checks := 0
	for offset := 0; offset < len(snapshot.Slots); offset++ {
		index := (start + offset) % len(snapshot.Slots)
		assignment := snapshot.Slots[index]
		if err := ctxErr(ctx); err != nil {
			return err
		}
		if assignment.SlotID == 0 || assignment.PreferredLeader == 0 ||
			!containsNode(assignment.DesiredPeers, r.cfg.LocalNode) {
			continue
		}

		status, err := r.cfg.Runtime.Status(multiraft.SlotID(assignment.SlotID))
		if err != nil {
			r.observeDecision(preferredLeaderDecisionError)
			r.cursor = (index + 1) % len(snapshot.Slots)
			return err
		}
		if uint64(status.LeaderID) != r.cfg.LocalNode || status.Term == 0 {
			r.clearAttempt(assignment.SlotID)
			continue
		}
		if status.LeaderID == multiraft.NodeID(assignment.PreferredLeader) {
			r.clearAttempt(assignment.SlotID)
			r.observeDecision(preferredLeaderDecisionMatch)
			continue
		}
		if !sameVoterSet(status.CurrentVoters, assignment.DesiredPeers) {
			r.clearAttempt(assignment.SlotID)
			r.observeDecision(preferredLeaderDecisionVoterMismatch)
			continue
		}

		fingerprint := preferredLeaderFingerprint{
			leader:      uint64(status.LeaderID),
			term:        status.Term,
			preferred:   assignment.PreferredLeader,
			configEpoch: assignment.ConfigEpoch,
		}
		if !r.reserveAttempt(assignment.SlotID, fingerprint) {
			r.observeDecision(preferredLeaderDecisionCooldown)
			continue
		}
		checks++
		intent := PreferredLeaderIntent{
			Revision:        snapshot.Revision,
			SlotID:          assignment.SlotID,
			ConfigEpoch:     assignment.ConfigEpoch,
			PreferredLeader: assignment.PreferredLeader,
			DesiredPeers:    append([]uint64(nil), assignment.DesiredPeers...),
		}
		intentGuard, current := multiraft.PreferredLeaderTransferGuard(nil), false
		if r.cfg.IntentGuard != nil {
			intentGuard, current = r.cfg.IntentGuard(intent)
		}
		if !current || intentGuard == nil || intentGuard.Context() == nil || intentGuard.Context().Err() != nil {
			r.clearAttempt(assignment.SlotID)
			r.observeDecision(preferredLeaderDecisionStaleIntent)
			r.cursor = (index + 1) % len(snapshot.Slots)
			if checks >= maxPreferredLeaderChecksPerReconcile {
				return nil
			}
			continue
		}
		if err := ctxErr(ctx); err != nil {
			return err
		}
		intentCtx := intentGuard.Context()
		attemptCtx, cancel := context.WithTimeout(intentCtx, r.cfg.AttemptTimeout)
		stopOuterCancel := context.AfterFunc(ctx, cancel)
		startedAt := time.Now()
		decision, err := r.cfg.Runtime.TryTransferLeadershipToPreferred(
			attemptCtx,
			multiraft.SlotID(assignment.SlotID),
			status.LeaderID,
			status.Term,
			toMultiRaftNodeIDs(assignment.DesiredPeers),
			multiraft.NodeID(assignment.PreferredLeader),
			intentGuard,
		)
		stopOuterCancel()
		cancel()
		r.cursor = (index + 1) % len(snapshot.Slots)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if intentCtx.Err() != nil {
				r.clearAttempt(assignment.SlotID)
				r.observeStrictWait(preferredLeaderDecisionStaleIntent, time.Since(startedAt))
				r.observeDecision(preferredLeaderDecisionStaleIntent)
				return nil
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
				r.observeStrictWait(preferredLeaderDecisionTimeout, time.Since(startedAt))
				r.observeDecision(preferredLeaderDecisionTimeout)
				return nil
			}
			r.observeStrictWait(preferredLeaderDecisionError, time.Since(startedAt))
			r.observeDecision(preferredLeaderDecisionError)
			return err
		}
		decisionLabel := string(decision)
		if decisionLabel == "" {
			decisionLabel = preferredLeaderDecisionError
		}
		r.observeStrictWait(decisionLabel, time.Since(startedAt))
		r.observeDecision(decisionLabel)
		if decision == multiraft.PreferredLeaderTransferStarted || checks >= maxPreferredLeaderChecksPerReconcile {
			return nil
		}
	}
	r.cursor = (start + 1) % len(snapshot.Slots)
	return nil
}

func (r *PreferredLeaderReconciler) observeDecision(decision string) {
	if r != nil && r.cfg.Observer != nil {
		r.cfg.Observer.ObservePreferredLeaderDecision(decision)
	}
}

func (r *PreferredLeaderReconciler) observeStrictWait(decision string, d time.Duration) {
	if r != nil && r.cfg.Observer != nil {
		r.cfg.Observer.ObservePreferredLeaderStrictWait(decision, d)
	}
}

func toMultiRaftNodeIDs(nodeIDs []uint64) []multiraft.NodeID {
	out := make([]multiraft.NodeID, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		out = append(out, multiraft.NodeID(nodeID))
	}
	return out
}

func (r *PreferredLeaderReconciler) reserveAttempt(slotID uint32, fingerprint preferredLeaderFingerprint) bool {
	now := r.cfg.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	previous, ok := r.attempts[slotID]
	if ok && previous.fingerprint == fingerprint && now.Sub(previous.at) < r.cfg.Cooldown {
		return false
	}
	r.attempts[slotID] = preferredLeaderAttempt{fingerprint: fingerprint, at: now}
	return true
}

func (r *PreferredLeaderReconciler) clearAttempt(slotID uint32) {
	r.mu.Lock()
	delete(r.attempts, slotID)
	r.mu.Unlock()
}

// pruneAttempts drops cooldown state for physical Slots absent from the current snapshot.
func (r *PreferredLeaderReconciler) pruneAttempts(assignments []control.SlotAssignment) {
	current := make(map[uint32]struct{}, len(assignments))
	for _, assignment := range assignments {
		if assignment.SlotID != 0 {
			current[assignment.SlotID] = struct{}{}
		}
	}
	r.mu.Lock()
	for slotID := range r.attempts {
		if _, ok := current[slotID]; !ok {
			delete(r.attempts, slotID)
		}
	}
	r.mu.Unlock()
}

func sameVoterSet(voters []multiraft.NodeID, desired []uint64) bool {
	if len(voters) != len(desired) {
		return false
	}
	seen := make(map[uint64]struct{}, len(voters))
	for _, voter := range voters {
		seen[uint64(voter)] = struct{}{}
	}
	if len(seen) != len(desired) {
		return false
	}
	for _, nodeID := range desired {
		if _, ok := seen[nodeID]; !ok {
			return false
		}
	}
	return true
}
