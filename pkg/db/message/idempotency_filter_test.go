package message

import (
	"fmt"
	"testing"
)

func TestIdempotencyFilterKeepsFewKeysSparseAndPromotesWithoutLoss(t *testing.T) {
	var filter idempotencyMembershipFilter
	keys := make([][]byte, 2000)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("sparse-client-message-%d", i))
		filter.add(keys[i])
		if i < 8 {
			filter.add(keys[i])
			if bytes := cap(filter.primary) * 8; bytes > 128 {
				t.Fatalf("%d committed keys retain %d primary bytes, want at most 128", i+1, bytes)
			}
		}
	}
	if len(filter.overflowBits) == 0 {
		t.Fatal("fixture did not exercise the overflow layer")
	}
	for i, key := range keys {
		if !filter.mayContain(key) {
			t.Fatalf("committed key %d was lost after promotion", i)
		}
	}
}

func TestIdempotencyMembershipFilterHasNoFalseNegativesAcrossOverflow(t *testing.T) {
	var filter idempotencyMembershipFilter
	if filter.mayContain([]byte("missing")) {
		t.Fatal("empty filter reported a possible hit")
	}

	const keys = idempotencyMembershipPrimaryCapacity + 512
	for i := 0; i < keys; i++ {
		key := []byte(fmt.Sprintf("idempotency-%06d", i))
		filter.add(key)
		if !filter.mayContain(key) {
			t.Fatalf("filter lost key %q after add", key)
		}
	}
	if len(filter.overflowBits) == 0 {
		t.Fatal("overflow filter was not allocated after the primary capacity")
	}

	for i := 0; i < keys; i++ {
		key := []byte(fmt.Sprintf("idempotency-%06d", i))
		if !filter.mayContain(key) {
			t.Fatalf("filter produced a false negative for key %q", key)
		}
	}
}
