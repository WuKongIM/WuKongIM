package message

import (
	"testing"
	"time"
)

func TestCommitCoordinatorUsesMeasuredBatchingDefault(t *testing.T) {
	got := effectiveCommitCoordinatorConfig(CommitCoordinatorConfig{}).FlushWindow
	want := 500 * time.Microsecond
	if got != want {
		t.Fatalf("default commit coordinator flush window = %s, want %s", got, want)
	}
}

func TestMessageEngineOptionsUseLargerMemTable(t *testing.T) {
	const wantMemTable = 64 << 20
	const wantCompactionDebtStep = 128 << 20
	opts := messageEngineOptions(nil)
	if got := opts.MemTableSize; got != wantMemTable {
		t.Fatalf("message MemTableSize = %d, want %d", got, wantMemTable)
	}
	if got := opts.CompactionDebtConcurrencyBytes; got != wantCompactionDebtStep {
		t.Fatalf("message CompactionDebtConcurrencyBytes = %d, want %d", got, wantCompactionDebtStep)
	}
}
