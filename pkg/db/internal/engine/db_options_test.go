package engine

import (
	"runtime"
	"testing"
)

func TestPebbleOptionsEnableBloomFiltersAtEveryLevel(t *testing.T) {
	opts := pebbleOptions(Options{})
	if len(opts.Levels) < 2 {
		t.Fatalf("Pebble levels = %d, want multiple levels", len(opts.Levels))
	}
	for level, options := range opts.Levels {
		if options.FilterPolicy == nil {
			t.Fatalf("level %d FilterPolicy = nil, want Bloom filter for negative point lookups", level)
		}
	}
}

func TestPebbleOptionsAvoidFrequentFullSSTableSyncsOnDarwin(t *testing.T) {
	opts := pebbleOptions(Options{})
	opts.EnsureDefaults()
	want := 512 << 10
	if runtime.GOOS == "darwin" {
		want = 16 << 20
	}
	if opts.BytesPerSync != want {
		t.Fatalf("BytesPerSync = %d, want %d on %s", opts.BytesPerSync, want, runtime.GOOS)
	}
}

func TestPebbleOptionsStartFourthCompactionBeforeL0WriteStop(t *testing.T) {
	opts := pebbleOptions(Options{})
	opts.EnsureDefaults()
	lower, upper := opts.CompactionConcurrencyRange()
	if lower != 1 || upper != 4 {
		t.Fatalf("CompactionConcurrencyRange = [%d,%d], want [1,4]", lower, upper)
	}
	lastL0Threshold := (upper - lower) * opts.Experimental.L0CompactionConcurrency
	if lastL0Threshold >= opts.L0StopWritesThreshold {
		t.Fatalf(
			"fourth L0 compaction threshold = %d, want below write-stop boundary %d",
			lastL0Threshold,
			opts.L0StopWritesThreshold,
		)
	}
	if lastL0Threshold != 18 {
		t.Fatalf("fourth L0 compaction threshold = %d, want 18", lastL0Threshold)
	}
}

func TestPebbleOptionsAllowDebtTriggeredFourthCompaction(t *testing.T) {
	opts := pebbleOptions(Options{})
	opts.EnsureDefaults()
	wantDebtStep := uint64(4) * opts.MemTableSize
	if opts.Experimental.CompactionDebtConcurrency != wantDebtStep {
		t.Fatalf(
			"CompactionDebtConcurrency = %d, want four memtables (%d)",
			opts.Experimental.CompactionDebtConcurrency,
			wantDebtStep,
		)
	}
}

func TestPebbleOptionsHonorExplicitCompactionDebtStep(t *testing.T) {
	const want = 128 << 20
	opts := pebbleOptions(Options{CompactionDebtConcurrencyBytes: want})
	opts.EnsureDefaults()
	if got := opts.Experimental.CompactionDebtConcurrency; got != want {
		t.Fatalf("CompactionDebtConcurrency = %d, want explicit %d", got, want)
	}
}
