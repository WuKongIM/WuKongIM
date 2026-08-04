package chatlifecycle

import (
	"errors"
	"math"
	"regexp"
	"testing"
)

func TestIdentityDecisionBelowRejectsBiasedPrefixDeterministically(t *testing.T) {
	space, err := NewIdentitySpace("relationship-test", 71, 3)
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}
	const purpose = "relationship-rejection-boundary-test/v1"
	const want = uint64(4_998_631_288_553_997_907)
	bound := uint64(1<<63) + 1
	threshold := -bound % bound
	firstDraw := space.decisionUint64(purpose, 17, 29)
	if firstDraw >= threshold {
		t.Fatalf("first draw %d does not exercise rejection prefix [0,%d)", firstDraw, threshold)
	}

	got, err := space.decisionBelow(purpose, bound, 17, 29)
	if err != nil {
		t.Fatalf("decisionBelow() error = %v", err)
	}
	if got != want || got >= bound {
		t.Fatalf("decisionBelow() = %d, want %d within [0,%d)", got, want, bound)
	}
	again, err := space.decisionBelow(purpose, bound, 17, 29)
	if err != nil || again != got {
		t.Fatalf("decisionBelow() again = %d, %v; want %d, nil", again, err, got)
	}
	if _, err := space.decisionBelow(purpose, 0, 17, 29); !errors.Is(err, errDecisionBoundRequired) {
		t.Fatalf("decisionBelow(zero bound) error = %v, want %v", err, errDecisionBoundRequired)
	}
}

func TestIdentitySpaceDerivesDeterministicSafeUIDs(t *testing.T) {
	space, err := NewIdentitySpace("formal/run @ 2026", 41, 3)
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}
	same, err := NewIdentitySpace("formal/run @ 2026", 41, 3)
	if err != nil {
		t.Fatalf("NewIdentitySpace(same) error = %v", err)
	}

	uid := space.UID(123_456)
	if uid != same.UID(123_456) {
		t.Fatalf("UID() = %q, same inputs = %q", uid, same.UID(123_456))
	}
	if len(uid) > MaxLifecycleUIDLength {
		t.Fatalf("UID length = %d, max = %d", len(uid), MaxLifecycleUIDLength)
	}
	if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(uid) {
		t.Fatalf("UID %q is not protocol-safe", uid)
	}
	if regexp.MustCompile(`[ /@]`).MatchString(uid) {
		t.Fatalf("UID %q leaked raw run ID characters", uid)
	}

	otherRun, _ := NewIdentitySpace("other-run", 41, 3)
	otherSeed, _ := NewIdentitySpace("formal/run @ 2026", 42, 3)
	if uid == otherRun.UID(123_456) || uid == otherSeed.UID(123_456) || uid == space.UID(123_457) {
		t.Fatalf("run, seed, and index must each change UID %q", uid)
	}
	if got, ok := space.IndexFromUID(uid); !ok || got != 123_456 {
		t.Fatalf("IndexFromUID(%q) = %d, %v; want 123456, true", uid, got, ok)
	}
	if _, ok := otherRun.IndexFromUID(uid); ok {
		t.Fatalf("other namespace accepted UID %q", uid)
	}
}

func TestIdentitySpaceInterleavesThreeWorkersWithoutGaps(t *testing.T) {
	space, err := NewIdentitySpace("three-workers", 9, 3)
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}

	const indexes = 1_000_000
	for globalIndex := uint64(0); globalIndex < indexes; globalIndex++ {
		workerID, localIndex := space.Owner(globalIndex)
		got, err := space.GlobalIndex(workerID, localIndex)
		if err != nil {
			t.Fatalf("GlobalIndex(%d, %d) error = %v", workerID, localIndex, err)
		}
		if got != globalIndex {
			t.Fatalf("GlobalIndex(%d, %d) = %d, want %d", workerID, localIndex, got, globalIndex)
		}
		if workerID != globalIndex%3 || localIndex != globalIndex/3 {
			t.Fatalf("Owner(%d) = (%d, %d), want (%d, %d)", globalIndex, workerID, localIndex, globalIndex%3, globalIndex/3)
		}
		uid := space.UID(got)
		uidIndex, ok := space.IndexFromUID(uid)
		if !ok || uidIndex != globalIndex {
			t.Fatalf("worker %d local index %d UID %q recovers %d/%v, want global index %d", workerID, localIndex, uid, uidIndex, ok, globalIndex)
		}
	}
}

func TestIdentitySpaceRejectsInvalidAndOverflowingIndexes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runID   string
		seed    uint64
		workers uint64
	}{
		{name: "empty run", seed: 1, workers: 3},
		{name: "zero seed", runID: "run", workers: 3},
		{name: "zero workers", runID: "run", seed: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewIdentitySpace(tc.runID, tc.seed, tc.workers); err == nil {
				t.Fatal("NewIdentitySpace() error = nil")
			}
		})
	}

	space, err := NewIdentitySpace("overflow", 1, 3)
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}
	if _, err := space.GlobalIndex(3, 0); err == nil {
		t.Fatal("GlobalIndex(invalid worker) error = nil")
	}
	if _, err := space.GlobalIndex(0, math.MaxUint64); err == nil {
		t.Fatal("GlobalIndex(overflowing local index) error = nil")
	}
}
