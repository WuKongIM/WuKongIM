package presence_test

import (
	"context"
	"testing"

	p "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// Failure-first: Full turns have no legacy recovered-slice census callback.
// Their unobserved structure fields must not claim measured zero ownership;
// mandatory budget gauges must still expose actual charged rows and bytes.
func TestSlotRecoveryStatsDistinguishesUnobservedLegacyCensus(t *testing.T) {
	slots := p.NewSlotRecovery(nil, nil, nil)
	r := p.NewAuthorityRecoveryWithOptions(slots, slots, p.RecoveryOptions{ResultCensus: true})
	t.Cleanup(func() {
		if err := r.Close(context.Background()); err != nil {
			t.Error(err)
		}
		if err := slots.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	lease, err := slots.RecoveryResultBudget().TryReserve(7, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	stats := r.Stats()
	if stats.ResultCensusEnabled {
		t.Fatal("Full turns claimed a legacy structure census without an observer")
	}
	if !stats.ResultBudgetEnabled || stats.OwnedResultRows != 7 || stats.OwnedResultBytes < 1024 {
		t.Fatalf("Full actual enforced budget was hidden: %+v", stats)
	}
	legacy := p.NewAuthorityRecoveryWithOptions(nil, nil, p.RecoveryOptions{ResultCensus: true})
	if !legacy.Stats().ResultCensusEnabled {
		t.Fatal("legacy requested census was disabled")
	}
	if err := legacy.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
