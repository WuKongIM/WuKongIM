package message

import "testing"

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
