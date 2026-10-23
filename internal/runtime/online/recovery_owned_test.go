package online_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
)

// The optional projection port fills prepaid caller storage. It does not return
// a second backing allocation or retain rows when a complete proof cannot fit.
type ownedRouteProjection interface {
	ActiveRoutesByUIDsInto([]string, []online.OwnerRoute) ([]online.OwnerRoute, bool)
}

func TestOwnedRecoveryProjectionUsesCallerStorageAndClearsSaturatedProof(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{})
	for i := uint64(1); i <= 3; i++ {
		s := online.LocalSession{Route: online.OwnerRoute{UID: "member", SessionID: i, OwnerNodeID: 1, OwnerBootID: 11}}
		if err := registry.RegisterPending(s); err != nil {
			t.Fatal(err)
		}
		if i != 3 {
			if err := registry.MarkActive(i); err != nil {
				t.Fatal(err)
			}
		}
	}
	reader, ok := any(registry).(ownedRouteProjection)
	if !ok {
		t.Fatal("registry cannot project active routes into prepaid caller storage")
	}
	backing := make([]online.OwnerRoute, 2)
	rows, complete := reader.ActiveRoutesByUIDsInto([]string{"member"}, backing[:0])
	if !complete || len(rows) != 2 || &rows[0] != &backing[0] {
		t.Fatal("complete active proof did not reuse caller-owned backing")
	}
	for _, row := range rows {
		if row.SessionID == 3 || row.UID != "member" {
			t.Fatal("pending or foreign session leaked into active proof")
		}
	}
	rows[0].UID = "caller-modified"
	clear(backing)
	rows, complete = reader.ActiveRoutesByUIDsInto([]string{"member"}, backing[:0])
	if !complete || len(rows) != 2 || rows[0].UID != "member" || rows[1].UID != "member" {
		t.Fatal("caller mutation changed registry ownership")
	}
	clear(backing)
	rows, complete = reader.ActiveRoutesByUIDsInto([]string{"member"}, backing[:0:1])
	if complete || len(rows) != 0 || backing[0] != (online.OwnerRoute{}) {
		t.Fatal("saturated proof retained partial route references")
	}
}
