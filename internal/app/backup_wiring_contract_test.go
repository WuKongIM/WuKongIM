package app

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func TestWireBackupRequiresTheCompleteClusterCapability(t *testing.T) {
	app := &App{
		cfg:     Config{DataDir: t.TempDir()},
		cluster: &backupNarrowCluster{},
		logger:  wklog.NewNop(),
	}
	if err := app.wireBackup(clusterpkg.Config{Control: clusterpkg.ControlConfig{ClusterID: "cluster-backup"}}); err != nil {
		t.Fatalf("wireBackup() with narrow cluster error = %v", err)
	}
	if app.backup != nil || app.scheduledBackup != nil || app.restore != nil || app.backupRuntime != nil {
		t.Fatalf("partial backup wiring = backup:%T scheduled:%T restore:%T runtime:%T, want all nil",
			app.backup, app.scheduledBackup, app.restore, app.backupRuntime)
	}
}

func TestWireBackupFailsClosedBeforePublishingAPartialCapability(t *testing.T) {
	node := newBackupWiringNode()
	app := &App{cluster: node, logger: wklog.NewNop()}

	err := app.wireBackup(clusterpkg.Config{NodeID: 1})
	if err == nil || !strings.Contains(err.Error(), "data directory and cluster identity are required") {
		t.Fatalf("wireBackup() error = %v, want required identity error", err)
	}
	if len(node.rpc) != 0 || app.backup != nil || app.scheduledBackup != nil || app.restore != nil || app.backupRuntime != nil {
		t.Fatalf("failed wiring published state: rpc=%d backup=%T scheduled=%T restore=%T runtime=%T",
			len(node.rpc), app.backup, app.scheduledBackup, app.restore, app.backupRuntime)
	}
}

func TestWireBackupPublishesManagementRestoreRuntimeAndAllNodeRPCsTogether(t *testing.T) {
	node := newBackupWiringNode()
	ids, err := newNodeMessageIDs(1)
	if err != nil {
		t.Fatalf("newNodeMessageIDs() error = %v", err)
	}
	dataDir := t.TempDir()
	app := &App{
		cfg:        Config{DataDir: dataDir, Manager: ManagerConfig{JWTSecret: "backup-installation-secret"}},
		cluster:    node,
		messageIDs: ids,
		logger:     wklog.NewNop(),
	}
	clusterConfig := clusterpkg.Config{
		NodeID:  1,
		DataDir: dataDir,
		Control: clusterpkg.ControlConfig{ClusterID: "cluster-backup"},
	}

	if err := app.wireBackup(clusterConfig); err != nil {
		t.Fatalf("wireBackup() error = %v", err)
	}
	if app.newBackupManagement() == nil || app.newRestoreManagement() == nil || app.scheduledBackup == nil || app.backupRuntime == nil {
		t.Fatalf("complete wiring = backup:%T restore:%T scheduled:%T runtime:%T, want all capabilities",
			app.newBackupManagement(), app.newRestoreManagement(), app.scheduledBackup, app.backupRuntime)
	}
	wantRPCs := []uint8{
		accessnode.ScheduledBackupSlotRPCServiceID,
		accessnode.ScheduledBackupMessageRPCServiceID,
		accessnode.ScheduledBackupRepositoryProbeRPCServiceID,
		accessnode.ScheduledBackupRestoreRPCServiceID,
	}
	if len(node.rpc) != len(wantRPCs) {
		t.Fatalf("registered RPC count = %d, want %d", len(node.rpc), len(wantRPCs))
	}
	for _, serviceID := range wantRPCs {
		if node.rpc[serviceID] == nil {
			t.Fatalf("backup RPC service %d was not registered", serviceID)
		}
	}
}

func TestWireBackupRejectsMissingMessageIDFenceWithoutAdvertisingRPCs(t *testing.T) {
	node := newBackupWiringNode()
	dataDir := t.TempDir()
	app := &App{
		cfg:     Config{DataDir: dataDir},
		cluster: node,
		logger:  wklog.NewNop(),
	}
	err := app.wireBackup(clusterpkg.Config{
		NodeID:  1,
		DataDir: dataDir,
		Control: clusterpkg.ControlConfig{ClusterID: "cluster-backup"},
	})
	if err == nil || !strings.Contains(err.Error(), "message ID allocator is required") {
		t.Fatalf("wireBackup() error = %v, want message ID allocator error", err)
	}
	if len(node.rpc) != 0 || app.backup != nil || app.scheduledBackup != nil || app.restore != nil || app.backupRuntime != nil {
		t.Fatalf("failed wiring advertised a partial capability: rpc=%d backup=%T scheduled=%T restore=%T runtime=%T",
			len(node.rpc), app.backup, app.scheduledBackup, app.restore, app.backupRuntime)
	}
}

func TestBackupIdentityIsPrefixedOpaqueAndUnique(t *testing.T) {
	first := newBackupIdentity("backup")
	second := newBackupIdentity("backup")
	if first == second {
		t.Fatalf("newBackupIdentity() repeated %q", first)
	}
	for _, identity := range []string{first, second} {
		body := strings.TrimPrefix(identity, "backup-")
		if body == identity {
			t.Fatalf("identity = %q, want backup prefix", identity)
		}
		decoded, err := hex.DecodeString(body)
		if err != nil || len(decoded) != 16 {
			t.Fatalf("identity body = %q, decoded bytes=%d error=%v; want 16 opaque bytes", body, len(decoded), err)
		}
	}
}

type backupNarrowCluster struct{}

func (*backupNarrowCluster) Start(context.Context) error { return nil }
func (*backupNarrowCluster) Stop(context.Context) error  { return nil }

// backupWiringNode embeds the large production capability only to model the
// optional composition seam. Construction is required to call only NodeID and
// RegisterRPC; unexpected operational calls therefore fail through the nil
// embedded interface instead of becoming permissive test behavior.
type backupWiringNode struct {
	appScheduledBackupNode
	rpc map[uint8]clusterpkg.NodeRPCHandler
}

func newBackupWiringNode() *backupWiringNode {
	return &backupWiringNode{rpc: make(map[uint8]clusterpkg.NodeRPCHandler)}
}

func (*backupWiringNode) Start(context.Context) error { return nil }
func (*backupWiringNode) Stop(context.Context) error  { return nil }
func (*backupWiringNode) NodeID() uint64              { return 1 }

func (n *backupWiringNode) RegisterRPC(serviceID uint8, handler clusterpkg.NodeRPCHandler) {
	if handler == nil {
		panic(errors.New("backup wiring registered a nil RPC handler"))
	}
	n.rpc[serviceID] = handler
}
