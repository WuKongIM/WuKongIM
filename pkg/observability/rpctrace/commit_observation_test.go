package rpctrace_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// The future narrow observation handle has no payload/context ownership. This
// draft is a contract compile RED until that approved Interface is introduced.
func TestCommitObservationAbsentOwnersRemainAllocationFree(t *testing.T) {
	if !rpctrace.NewCommitObservation().Empty() {
		t.Fatal("empty owners created an observation")
	}
	if !rpctrace.NewCommitObservation(nil).Empty() {
		t.Fatal("nil owner created an observation")
	}
	if got := testing.AllocsPerRun(100, func() {
		if !rpctrace.NewCommitObservation(nil).Empty() {
			panic("unexpected observation")
		}
	}); got != 0 {
		t.Fatalf("disabled observation allocated %v times", got)
	}
}
