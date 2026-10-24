package cluster

import (
	"context"
	"testing"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

type classifiedNodeRPC struct{}

func (classifiedNodeRPC) HandleRPC(context.Context, []byte) ([]byte, error) { return nil, nil }
func (classifiedNodeRPC) CancelRunningFor(b []byte) bool                    { return len(b) == 1 && b[0] == 1 }

func TestNodeRegistrationPreservesTrustedRequestPolicy(t *testing.T) {
	node, err := New(Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	node.RegisterRPC(clusternet.RPCPresenceAuthority, classifiedNodeRPC{})
	policy, ok := node.pendingRPCHandlers[clusternet.RPCPresenceAuthority].(interface{ CancelRunningFor([]byte) bool })
	if !ok || !policy.CancelRunningFor([]byte{1}) || policy.CancelRunningFor([]byte{2}) {
		t.Fatal("registration discarded the trusted read/mutation policy")
	}
	if len(node.pendingRPCHandlers) != 1 {
		t.Fatal("policy added a service registration")
	}
}
