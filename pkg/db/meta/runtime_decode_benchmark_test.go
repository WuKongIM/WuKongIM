package meta

import (
	"reflect"
	"testing"
)

func runtimeDecodeFixture() ([]byte, []byte) {
	key := []byte("runtime-read-benchmark")
	return key, encodeChannelRuntimeMetaValue(key, ChannelRuntimeMeta{ChannelEpoch: 3, LeaderEpoch: 4, Leader: 1, MinISR: 2, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}})
}

// Decoding owns the replica slices already; canonicalization must not copy and
// reflectively sort each freshly allocated set a second time.
func TestRuntimeMetadataDecodeAllocationBudget(t *testing.T) {
	key, value := runtimeDecodeFixture()
	allocations := testing.AllocsPerRun(20, func() {
		if _, err := decodeChannelRuntimeMetaValue(key, value); err != nil {
			t.Fatal(err)
		}
	})
	if allocations > 4 {
		t.Fatalf("runtime decode allocations = %.0f, want <= 4", allocations)
	}
}

func TestRuntimeMetadataNormalizationPreservesCallerSlices(t *testing.T) {
	input := ChannelRuntimeMeta{ChannelType: 1, ChannelEpoch: 3, Replicas: []uint64{3, 1, 3, 2}, ISR: []uint64{2, 1, 2}}
	got := NormalizeChannelRuntimeMeta(input)
	if !reflect.DeepEqual(got.Replicas, []uint64{1, 2, 3}) || !reflect.DeepEqual(got.ISR, []uint64{1, 2}) || got.RouteGeneration != 3 || got.DirectoryGeneration != 1 {
		t.Fatalf("canonical metadata = %+v", got)
	}
	got.Replicas[0], got.ISR[0] = 99, 99
	if !reflect.DeepEqual(input.Replicas, []uint64{3, 1, 3, 2}) || !reflect.DeepEqual(input.ISR, []uint64{2, 1, 2}) {
		t.Fatal("normalization changed caller-owned slices")
	}
}

func BenchmarkRuntimeMetadataDecode(b *testing.B) {
	key, value := runtimeDecodeFixture()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := decodeChannelRuntimeMetaValue(key, value); err != nil {
			b.Fatal(err)
		}
	}
}
