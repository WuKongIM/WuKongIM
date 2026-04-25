package slotmigration

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestProgressIsStable(t *testing.T) {
	p := &Progress{
		DeltaLag:          50,
		StableWindowStart: time.Now().Add(-time.Second),
	}
	if !p.IsStable(100, 500*time.Millisecond) {
		t.Fatal("IsStable() = false, want true")
	}
	if p.IsStable(10, 500*time.Millisecond) {
		t.Fatal("IsStable() = true, want false when lag exceeds threshold")
	}
}

func TestWorkerMigrationPhaseTransitions(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	worker := NewWorker(100, 200*time.Millisecond)
	worker.now = func() time.Time { return now }

	if err := worker.StartMigration(5, 2, 4); err != nil {
		t.Fatalf("StartMigration() error = %v", err)
	}

	migration, ok := worker.migrations[5]
	if !ok {
		t.Fatal("migration missing after StartMigration")
	}
	if migration.phase != PhaseSnapshot {
		t.Fatalf("phase after StartMigration = %v, want %v", migration.phase, PhaseSnapshot)
	}
	if migration.source != multiraft.SlotID(2) || migration.target != multiraft.SlotID(4) {
		t.Fatalf("migration = %#v", migration)
	}

	if got := worker.Tick(); len(got) != 0 {
		t.Fatalf("transitions before snapshot completion = %#v, want none", got)
	}
	if migration.phase != PhaseSnapshot {
		t.Fatalf("phase before snapshot completion = %v, want %v", migration.phase, PhaseSnapshot)
	}

	if err := worker.MarkSnapshotComplete(5, 42, 2048); err != nil {
		t.Fatalf("MarkSnapshotComplete() error = %v", err)
	}
	transitions := worker.Tick()
	if len(transitions) != 1 || transitions[0].To != PhaseDelta {
		t.Fatalf("snapshot transitions = %#v", transitions)
	}
	if migration.phase != PhaseDelta {
		t.Fatalf("phase after snapshot tick = %v, want %v", migration.phase, PhaseDelta)
	}
	if migration.snapshotAt != 42 {
		t.Fatalf("snapshotAt = %d, want 42", migration.snapshotAt)
	}

	if err := worker.UpdateProgress(5, 250, 10, 4096); err != nil {
		t.Fatalf("UpdateProgress(unstable) error = %v", err)
	}
	if !migration.progress.StableWindowStart.IsZero() {
		t.Fatalf("stable window after unstable update = %v, want zero", migration.progress.StableWindowStart)
	}

	now = now.Add(time.Second)
	if err := worker.UpdateProgress(5, 50, 50, 8192); err != nil {
		t.Fatalf("UpdateProgress(stable) error = %v", err)
	}
	now = now.Add(time.Second)
	transitions = worker.Tick()
	if len(transitions) != 1 || transitions[0].To != PhaseSwitching {
		t.Fatalf("delta transitions = %#v", transitions)
	}
	if migration.phase != PhaseSwitching {
		t.Fatalf("phase after delta tick = %v, want %v", migration.phase, PhaseSwitching)
	}
	if migration.progress.DeltaLag != 0 {
		t.Fatalf("delta lag = %d, want 0", migration.progress.DeltaLag)
	}

	if got := worker.Tick(); len(got) != 0 {
		t.Fatalf("switching transitions before completion = %#v, want none", got)
	}
	if migration.phase != PhaseSwitching {
		t.Fatalf("phase before switch completion = %v, want %v", migration.phase, PhaseSwitching)
	}

	if err := worker.MarkSwitchComplete(5); err != nil {
		t.Fatalf("MarkSwitchComplete() error = %v", err)
	}
	transitions = worker.Tick()
	if len(transitions) != 1 || transitions[0].To != PhaseDone {
		t.Fatalf("switch transitions = %#v", transitions)
	}
	if migration.phase != PhaseDone {
		t.Fatalf("phase after switching tick = %v, want %v", migration.phase, PhaseDone)
	}

	worker.Tick()
	if _, ok := worker.migrations[5]; ok {
		t.Fatal("migration still present after done cleanup")
	}
}

func TestWorkerTickTimesOutStalledMigration(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	worker := NewWorker(100, time.Second)
	worker.stallTimeout = 2 * time.Second
	worker.now = func() time.Time { return now }

	if err := worker.StartMigration(5, 2, 4); err != nil {
		t.Fatalf("StartMigration() error = %v", err)
	}

	now = now.Add(3 * time.Second)
	transitions := worker.Tick()
	if len(transitions) != 1 {
		t.Fatalf("Tick() transitions = %#v, want 1 timeout transition", transitions)
	}
	if !transitions[0].TimedOut {
		t.Fatalf("Tick() transition = %#v, want timeout event", transitions[0])
	}
	if transitions[0].HashSlot != 5 || transitions[0].Source != 2 || transitions[0].Target != 4 {
		t.Fatalf("Tick() transition = %#v, want hash slot 5 source 2 target 4", transitions[0])
	}
	if _, ok := worker.migrations[5]; ok {
		t.Fatal("migration still present after timeout tick")
	}
}

func TestWorkerUpdateProgressReturnsErrorForUnknownMigration(t *testing.T) {
	worker := NewWorker(100, time.Second)
	if err := worker.UpdateProgress(7, 10, 10, 1); err == nil {
		t.Fatal("UpdateProgress() error = nil, want error for unknown migration")
	}
	if err := worker.MarkSnapshotComplete(7, 10, 1); err == nil {
		t.Fatal("MarkSnapshotComplete() error = nil, want error for unknown migration")
	}
	if err := worker.MarkSwitchComplete(7); err == nil {
		t.Fatal("MarkSwitchComplete() error = nil, want error for unknown migration")
	}
}

func TestWorkerStartMigrationLimitsTotalConcurrency(t *testing.T) {
	worker := NewWorker(100, time.Second)

	for i := 0; i < 4; i++ {
		if err := worker.StartMigration(uint16(i+1), multiraft.SlotID(i+1), multiraft.SlotID(i+10)); err != nil {
			t.Fatalf("StartMigration(%d) error = %v", i+1, err)
		}
	}

	if err := worker.StartMigration(5, 5, 15); err != nil {
		t.Fatalf("StartMigration(fifth) error = %v", err)
	}

	if got := len(worker.ActiveMigrations()); got != 4 {
		t.Fatalf("ActiveMigrations() len = %d, want 4", got)
	}
	if _, ok := worker.migrations[5]; ok {
		t.Fatal("migration 5 activated despite total concurrency limit")
	}

	if err := worker.AbortMigration(1); err != nil {
		t.Fatalf("AbortMigration() error = %v", err)
	}
	if err := worker.StartMigration(5, 5, 15); err != nil {
		t.Fatalf("StartMigration(retry fifth) error = %v", err)
	}
	if _, ok := worker.migrations[5]; !ok {
		t.Fatal("migration 5 not activated after a concurrency slot was freed")
	}
}

func TestWorkerStartMigrationLimitsPerSourceSlot(t *testing.T) {
	worker := NewWorker(100, time.Second)

	if err := worker.StartMigration(1, 2, 20); err != nil {
		t.Fatalf("StartMigration(first) error = %v", err)
	}
	if err := worker.StartMigration(2, 2, 21); err != nil {
		t.Fatalf("StartMigration(second) error = %v", err)
	}
	if err := worker.StartMigration(3, 3, 22); err != nil {
		t.Fatalf("StartMigration(other source) error = %v", err)
	}

	if err := worker.StartMigration(4, 2, 23); err != nil {
		t.Fatalf("StartMigration(third from source) error = %v", err)
	}

	if got := len(worker.ActiveMigrations()); got != 3 {
		t.Fatalf("ActiveMigrations() len = %d, want 3", got)
	}
	if _, ok := worker.migrations[4]; ok {
		t.Fatal("migration 4 activated despite per-source concurrency limit")
	}

	if err := worker.AbortMigration(1); err != nil {
		t.Fatalf("AbortMigration() error = %v", err)
	}
	if err := worker.StartMigration(4, 2, 23); err != nil {
		t.Fatalf("StartMigration(retry third from source) error = %v", err)
	}
	if _, ok := worker.migrations[4]; !ok {
		t.Fatal("migration 4 not activated after a source slot concurrency slot was freed")
	}
}
