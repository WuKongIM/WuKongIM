package app

import (
	"context"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

type opsMCPWiringCluster struct {
	handlers map[uint8]cluster.NodeRPCHandler
}

func (c *opsMCPWiringCluster) Start(context.Context) error { return nil }
func (c *opsMCPWiringCluster) Stop(context.Context) error  { return nil }
func (c *opsMCPWiringCluster) NodeID() uint64              { return 1 }
func (c *opsMCPWiringCluster) ClusterID() string           { return "cluster-ops-mcp" }

func (c *opsMCPWiringCluster) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return control.Snapshot{ClusterID: "cluster-ops-mcp", Revision: 1}, nil
}

func (c *opsMCPWiringCluster) RegisterRPC(serviceID uint8, handler cluster.NodeRPCHandler) {
	if c.handlers == nil {
		c.handlers = make(map[uint8]cluster.NodeRPCHandler)
	}
	c.handlers[serviceID] = handler
}

func (c *opsMCPWiringCluster) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	return c.handlers[serviceID].HandleRPC(ctx, payload)
}

func TestWireOpsMCPMountsEndpointAndRegistersTypedRPC(t *testing.T) {
	clusterRuntime := &opsMCPWiringCluster{}
	app := &App{
		cfg: Config{
			Log:     LogConfig{Dir: t.TempDir()},
			Cluster: cluster.Config{Control: cluster.ControlConfig{ClusterID: "cluster-ops-mcp"}},
		},
		cluster: clusterRuntime,
	}
	app.ensureOpsMCPCallControl()
	t.Cleanup(func() { _ = app.closeOpsMCPCalls() })
	management := app.newManagerManagement()

	app.wireOpsMCP(management)

	if app.opsMCPEndpoint == nil {
		t.Fatal("opsMCPEndpoint = nil")
	}
	if clusterRuntime.handlers[accessnode.OpsMCPRPCServiceID] == nil {
		t.Fatalf("RPC service %d was not registered", accessnode.OpsMCPRPCServiceID)
	}
}

func TestWireOpsMCPRestoreModeDoesNotRegisterEndpointOrRPC(t *testing.T) {
	clusterRuntime := &opsMCPWiringCluster{}
	app := &App{
		cfg: Config{
			Log:     LogConfig{Dir: t.TempDir()},
			Backup:  BackupConfig{RestoreMode: true},
			Cluster: cluster.Config{Control: cluster.ControlConfig{ClusterID: "cluster-ops-mcp"}},
		},
		cluster: clusterRuntime,
	}
	app.ensureOpsMCPCallControl()
	t.Cleanup(func() { _ = app.closeOpsMCPCalls() })
	management := app.newManagerManagement()

	app.wireOpsMCP(management)

	if app.opsMCPEndpoint != nil || clusterRuntime.handlers[accessnode.OpsMCPRPCServiceID] != nil {
		t.Fatal("restore-only Manager wired Operations MCP")
	}
}
