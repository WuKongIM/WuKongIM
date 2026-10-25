package engine

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/cockroachdb/pebble/v2"
)

// The read-cache target must survive ordinary writable/recycled memtable
// reservations without changing the chosen memtable or write-stop threshold.
func TestPebbleOptionsBudgetReadCacheBeyondMemtableReservations(t *testing.T) {
	for _, tc := range []struct {
		name            string
		input           Options
		cache, memtable int64
	}{
		{"defaults", Options{}, 320 << 20, 32 << 20},
		{"message", Options{CacheSize: 128 << 20, MemTableSize: 64 << 20}, 512 << 20, 64 << 20},
		{"explicit_small", Options{CacheSize: 8 << 20, MemTableSize: 4 << 20}, 32 << 20, 4 << 20},
		{"nonpositive_defaults", Options{CacheSize: -1, MemTableSize: -1}, 320 << 20, 32 << 20},
		{"readonly_replay", Options{CacheSize: 128 << 20, MemTableSize: 64 << 20, ReadOnly: true}, 512 << 20, 64 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mustPebbleOptions(t, tc.input)
			if got.CacheSize != tc.cache {
				t.Errorf("combined cache budget = %d, want %d", got.CacheSize, tc.cache)
			}
			if got.MemTableSize != uint64(tc.memtable) || got.MemTableStopWritesThreshold != 4 || got.ReadOnly != tc.input.ReadOnly {
				t.Fatalf("memory budgeting changed memtable, write-stop or read-only policy")
			}
		})
	}
}

// Invalid budgets must fail before Pebble can interpret overflow as a default
// or create a cache whose reservations cannot be represented by native ints.
func TestPebbleOptionsRejectUnrepresentableMemoryBudgets(t *testing.T) {
	maxInt := int64(^uint(0) >> 1)
	for _, input := range []Options{
		{CacheSize: maxInt, MemTableSize: 1},
		{CacheSize: 1, MemTableSize: maxInt/6 + 1},
		{CacheSize: maxInt - 5, MemTableSize: 1},
	} {
		got, err := pebbleOptions(input, nil)
		if !errors.Is(err, dberrors.ErrInvalidArgument) || got != nil {
			t.Fatalf("unrepresentable budget: options=%v error=%v", got, err)
		}
	}
	got := mustPebbleOptions(t, Options{CacheSize: maxInt - 6, MemTableSize: 1})
	if got.CacheSize != maxInt {
		t.Fatalf("representable boundary = %d, want %d", got.CacheSize, maxInt)
	}
}

func mustPebbleOptions(t *testing.T, input Options) *pebble.Options {
	t.Helper()
	opts, err := pebbleOptions(input, nil)
	if err != nil {
		t.Fatal(err)
	}
	return opts
}
