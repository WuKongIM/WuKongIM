package backup_test

import (
	"testing"

	backupruntime "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
)

func TestGenerationCompactionBudgetBoundsConcurrencyIOAndNetwork(t *testing.T) {
	budget, err := backupruntime.NewGenerationCompactionBudget(1, 100, 200)
	if err != nil {
		t.Fatalf("NewGenerationCompactionBudget() error = %v", err)
	}
	cost := backupruntime.GenerationCompactionCost{IOBytes: 100, NetworkBytes: 200}
	if !budget.TryAcquire(cost) {
		t.Fatal("first TryAcquire() = false")
	}
	if budget.TryAcquire(backupruntime.GenerationCompactionCost{IOBytes: 1, NetworkBytes: 1}) {
		t.Fatal("concurrent TryAcquire() = true")
	}
	budget.Release(cost)
	if !budget.TryAcquire(cost) {
		t.Fatal("TryAcquire() after release = false")
	}
	budget.Release(cost)
	oversized := backupruntime.GenerationCompactionCost{IOBytes: 1_000, NetworkBytes: 2_000}
	if !budget.TryAcquire(oversized) {
		t.Fatal("exclusive oversized TryAcquire() = false")
	}
	if budget.TryAcquire(backupruntime.GenerationCompactionCost{IOBytes: 1, NetworkBytes: 1}) {
		t.Fatal("concurrent TryAcquire() beside oversized work = true")
	}
	budget.Release(oversized)
}
