package meta

import (
	"math/rand"
	"testing"
)

// Keep the range-based definition as an independent oracle for malformed keys
// and namespace boundaries, as well as valid snapshot entries.
func snapshotKeyInSpans(key []byte, slots []HashSlot) bool {
	for _, slot := range slots {
		for _, span := range hashSlotAllDataSpans(slot) {
			if bytesInSpan(key, span) {
				return true
			}
		}
	}
	return false
}

func TestSnapshotKeyOwnershipMatchesSpanBoundaries(t *testing.T) {
	slots := []HashSlot{65535, 0, 11, 255} // Membership must not require sorted input.
	check := func(key []byte) {
		t.Helper()
		if got, want := snapshotEntryInHashSlots(key, slots), snapshotKeyInSpans(key, slots); got != want {
			t.Fatalf("key %x ownership = %v, want %v", key, got, want)
		}
	}
	for _, slot := range []HashSlot{0, 1, 10, 11, 12, 254, 255, 256, 65534, 65535} {
		for _, span := range hashSlotAllDataSpans(slot) {
			for _, boundary := range [][]byte{span.Start, span.End} {
				for i := 0; i <= len(boundary); i++ {
					check(boundary[:i])
				}
				check(append(append([]byte(nil), boundary...), 0, 255))
				for i := range boundary {
					for v := 0; v < 256; v++ {
						key := append([]byte(nil), boundary...)
						key[i] = byte(v)
						check(key)
					}
				}
			}
		}
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 1000; i++ {
		key := make([]byte, rng.Intn(32))
		_, _ = rng.Read(key)
		check(key)
	}
}

func TestSnapshotKeyOwnershipDoesNotAllocatePerRow(t *testing.T) {
	slots := make([]HashSlot, 256)
	for i := range slots {
		slots[i] = HashSlot(i)
	}
	key := encodeUserRowKey(255, "last-hash-slot", userPrimaryFamilyID)
	if allocations := testing.AllocsPerRun(10, func() {
		if !snapshotEntryInHashSlots(key, slots) {
			panic("owned key rejected")
		}
	}); allocations != 0 {
		t.Fatalf("ownership check allocated %g objects per row, want 0", allocations)
	}
}

func BenchmarkSnapshotKeyOwnership(b *testing.B) {
	slots := make([]HashSlot, 22) // One physical Slot in a 12/256 deployment.
	for i := range slots {
		slots[i] = HashSlot(i * 12)
	}
	key := encodeUserRowKey(slots[len(slots)-1], "snapshot-user", userPrimaryFamilyID)
	for _, tc := range []struct {
		name  string
		check func([]byte, []HashSlot) bool
	}{{"span_oracle", snapshotKeyInSpans}, {"ownership", snapshotEntryInHashSlots}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if !tc.check(key, slots) {
					b.Fatal("owned key rejected")
				}
			}
		})
	}
}
