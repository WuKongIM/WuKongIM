package app

import (
	"context"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

type recoveryWiringCluster struct{ *fakePresenceCluster }

func (*recoveryWiringCluster) LocalControllerSnapshot(context.Context) (control.Snapshot, error) {
	return control.Snapshot{}, nil
}

func TestPresenceCompositionOwnsRecoveryAndClassifiesMixedServices(t *testing.T) {
	cluster := &recoveryWiringCluster{newFakePresenceCluster(1, nil)}
	a, err := newTestApp(t, Config{Cluster: clusterpkg.Config{NodeID: 1}}, WithCluster(cluster))
	if err != nil {
		t.Fatal(err)
	}
	if a.presenceRecovery == nil || a.reopenPresenceRecovery == nil {
		t.Error("recovery lifecycle not owned by composition")
	}
	for _, id := range []uint8{accessnode.PresenceAuthorityRPCServiceID, accessnode.PresenceOwnerRPCServiceID} {
		policy, ok := cluster.registeredHandlers[id].(interface{ CancelRunningFor([]byte) bool })
		if !ok {
			t.Errorf("mixed service %d discarded classification", id)
			continue
		}
		for opcode := byte(0); opcode < 14; opcode++ {
			want := (id == accessnode.PresenceAuthorityRPCServiceID && (opcode == 5 || opcode == 8)) || (id == accessnode.PresenceOwnerRPCServiceID && (opcode == 9 || opcode == 10 || opcode == 11))
			if got := policy.CancelRunningFor([]byte{'W', 'K', 'V', 'P', 2, opcode}); got != want {
				t.Errorf("service=%d opcode=%d policy=%v want=%v", id, opcode, got, want)
			}
		}
		if policy.CancelRunningFor(nil) || policy.CancelRunningFor([]byte{'W', 'K', 'V', 'P', 1, 5}) {
			t.Error("unrecognized header classified read-only")
		}
	}
}
