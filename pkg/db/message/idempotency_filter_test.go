package message

import (
	"fmt"
	"testing"
)

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
