package cluster

import (
	"encoding/binary"
	"sort"
	"sync"
)

// observationHint is a best-effort wake signal emitted by the controller leader.
type observationHint struct {
	LeaderID         uint64
	LeaderGeneration uint64
	Revisions        observationRevisions
	AffectedSlots    []uint32
	NeedFullSync     bool
}

type observationWakeSnapshot struct {
	Pending bool
	Hint    observationHint
}

// observationWakeState coalesces hint-driven wakeups on followers.
type observationWakeState struct {
	mu      sync.Mutex
	pending bool
	hint    observationHint
}

func encodeObservationHint(hint observationHint) []byte {
	body := make([]byte, 0, 8*6+1+len(hint.AffectedSlots)*4)
	body = binary.BigEndian.AppendUint64(body, hint.LeaderID)
	body = binary.BigEndian.AppendUint64(body, hint.LeaderGeneration)
	body = appendObservationRevisions(body, hint.Revisions)
	if hint.NeedFullSync {
		body = append(body, 1)
	} else {
		body = append(body, 0)
	}
	return appendUint32Slice(body, hint.AffectedSlots)
}

func decodeObservationHint(body []byte) (observationHint, error) {
	leaderID, rest, err := readUint64(body)
	if err != nil {
		return observationHint{}, err
	}
	leaderGeneration, rest, err := readUint64(rest)
	if err != nil {
		return observationHint{}, err
	}
	revisions, rest, err := readObservationRevisions(rest)
	if err != nil {
		return observationHint{}, err
	}
	if len(rest) < 1 {
		return observationHint{}, ErrInvalidConfig
	}
	needFullSync := rest[0] == 1
	affectedSlots, rest, err := readUint32Slice(rest[1:])
	if err != nil || len(rest) != 0 {
		return observationHint{}, ErrInvalidConfig
	}
	return observationHint{
		LeaderID:         leaderID,
		LeaderGeneration: leaderGeneration,
		Revisions:        revisions,
		AffectedSlots:    affectedSlots,
		NeedFullSync:     needFullSync,
	}, nil
}

func newObservationWakeState() *observationWakeState {
	return &observationWakeState{}
}

func (s *observationWakeState) reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = false
	s.hint = observationHint{}
}

func (s *observationWakeState) snapshot() observationWakeSnapshot {
	if s == nil {
		return observationWakeSnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return observationWakeSnapshot{
		Pending: s.pending,
		Hint:    cloneObservationHint(s.hint),
	}
}

func (s *observationWakeState) takePending() (observationWakeSnapshot, bool) {
	if s == nil {
		return observationWakeSnapshot{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pending {
		return observationWakeSnapshot{}, false
	}
	snapshot := observationWakeSnapshot{
		Pending: true,
		Hint:    cloneObservationHint(s.hint),
	}
	s.pending = false
	return snapshot, true
}

func (s *observationWakeState) observeHint(currentLeaderID uint64, hint observationHint) bool {
	if s == nil || hint.LeaderID == 0 || hint.LeaderGeneration == 0 {
		return false
	}
	if currentLeaderID != 0 && hint.LeaderID != currentLeaderID {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.hint.LeaderID != 0 {
		if hint.LeaderID == s.hint.LeaderID && hint.LeaderGeneration < s.hint.LeaderGeneration {
			return false
		}
		if hint.LeaderID != s.hint.LeaderID || hint.LeaderGeneration > s.hint.LeaderGeneration {
			s.hint = cloneObservationHint(hint)
			s.pending = true
			return true
		}
	}

	merged := cloneObservationHint(s.hint)
	changed := false
	if merged.LeaderID == 0 {
		merged = cloneObservationHint(hint)
		changed = true
	} else {
		revisions := maxObservationRevisions(merged.Revisions, hint.Revisions)
		if revisions != merged.Revisions {
			merged.Revisions = revisions
			changed = true
		}
		if hint.NeedFullSync && !merged.NeedFullSync {
			merged.NeedFullSync = true
			changed = true
		}
		affectedSlots := unionObservationSlots(merged.AffectedSlots, hint.AffectedSlots)
		if !equalUint32Slices(affectedSlots, merged.AffectedSlots) {
			merged.AffectedSlots = affectedSlots
			changed = true
		}
	}
	if !s.pending {
		s.pending = true
		changed = true
	}
	if changed {
		s.hint = merged
	}
	return changed
}

func cloneObservationHint(hint observationHint) observationHint {
	hint.AffectedSlots = append([]uint32(nil), hint.AffectedSlots...)
	return hint
}

func maxObservationRevisions(left, right observationRevisions) observationRevisions {
	if right.Assignments > left.Assignments {
		left.Assignments = right.Assignments
	}
	if right.Tasks > left.Tasks {
		left.Tasks = right.Tasks
	}
	if right.Nodes > left.Nodes {
		left.Nodes = right.Nodes
	}
	if right.Runtime > left.Runtime {
		left.Runtime = right.Runtime
	}
	return left
}

func unionObservationSlots(left, right []uint32) []uint32 {
	if len(left) == 0 && len(right) == 0 {
		return nil
	}
	seen := make(map[uint32]struct{}, len(left)+len(right))
	out := make([]uint32, 0, len(left)+len(right))
	for _, slotID := range left {
		if slotID == 0 {
			continue
		}
		if _, ok := seen[slotID]; ok {
			continue
		}
		seen[slotID] = struct{}{}
		out = append(out, slotID)
	}
	for _, slotID := range right {
		if slotID == 0 {
			continue
		}
		if _, ok := seen[slotID]; ok {
			continue
		}
		seen[slotID] = struct{}{}
		out = append(out, slotID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func equalUint32Slices(left, right []uint32) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func hintFromObservationDelta(leaderID uint64, leaderGeneration uint64, delta observationDeltaResponse) observationHint {
	return observationHint{
		LeaderID:         leaderID,
		LeaderGeneration: leaderGeneration,
		Revisions:        delta.Revisions,
		AffectedSlots:    affectedSlotsFromObservationDelta(delta),
		NeedFullSync:     delta.FullSync,
	}
}

func affectedSlotsFromObservationDelta(delta observationDeltaResponse) []uint32 {
	out := make([]uint32, 0, len(delta.Assignments)+len(delta.Tasks)+len(delta.RuntimeViews)+len(delta.DeletedTasks)+len(delta.DeletedRuntimeSlots))
	for _, assignment := range delta.Assignments {
		out = append(out, assignment.SlotID)
	}
	for _, task := range delta.Tasks {
		out = append(out, task.SlotID)
	}
	for _, view := range delta.RuntimeViews {
		out = append(out, view.SlotID)
	}
	out = append(out, delta.DeletedTasks...)
	out = append(out, delta.DeletedRuntimeSlots...)
	return unionObservationSlots(nil, out)
}
