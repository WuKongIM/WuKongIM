package clusterv2

import (
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

func validNodeConfig(t *testing.T) Config {
	t.Helper()
	return Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()}
}

func freeTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func nodeControlSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision:     1,
		ControllerID: 1,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "127.0.0.1:1001", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 2, Addr: "127.0.0.1:1002", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 3, Addr: "127.0.0.1:1003", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots:     []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, PreferredLeader: 1}},
		HashSlots: control.HashSlotTable{Revision: 1, Count: 4, Ranges: []control.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
	}
}

func waitUntil(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
