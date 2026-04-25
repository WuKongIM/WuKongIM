package cluster

import (
	"context"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

type API interface {
	Start() error
	Stop()

	NodeID() multiraft.NodeID
	IsLocal(nodeID multiraft.NodeID) bool

	SlotForKey(key string) multiraft.SlotID
	HashSlotForKey(key string) uint16
	HashSlotsOf(slotID multiraft.SlotID) []uint16
	HashSlotTableVersion() uint64
	ControllerLeaderID() uint64
	LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error)
	Propose(ctx context.Context, slotID multiraft.SlotID, cmd []byte) error

	SlotIDs() []multiraft.SlotID
	PeersForSlot(slotID multiraft.SlotID) []multiraft.NodeID
	WaitForManagedSlotsReady(ctx context.Context) error

	ListNodes(ctx context.Context) ([]controllermeta.ClusterNode, error)
	// ListNodesStrict returns the controller leader's node snapshot without local fallback.
	ListNodesStrict(ctx context.Context) ([]controllermeta.ClusterNode, error)
	ListSlotAssignments(ctx context.Context) ([]controllermeta.SlotAssignment, error)
	// ListSlotAssignmentsStrict returns the controller leader's slot assignments without local fallback.
	ListSlotAssignmentsStrict(ctx context.Context) ([]controllermeta.SlotAssignment, error)
	// ListObservedRuntimeViews returns the controller leader's observed runtime snapshot when available.
	ListObservedRuntimeViews(ctx context.Context) ([]controllermeta.SlotRuntimeView, error)
	// ListObservedRuntimeViewsStrict returns the controller leader's observed runtime snapshot without local fallback.
	ListObservedRuntimeViewsStrict(ctx context.Context) ([]controllermeta.SlotRuntimeView, error)
	ListTasks(ctx context.Context) ([]controllermeta.ReconcileTask, error)
	// ListTasksStrict returns the controller leader's task snapshot without local fallback.
	ListTasksStrict(ctx context.Context) ([]controllermeta.ReconcileTask, error)
	GetMigrationStatus() []HashSlotMigration
	TransportPoolStats() []transport.PoolPeerStats
	GetReconcileTask(ctx context.Context, slotID uint32) (controllermeta.ReconcileTask, error)
	// GetReconcileTaskStrict returns the controller leader's task detail without local fallback.
	GetReconcileTaskStrict(ctx context.Context, slotID uint32) (controllermeta.ReconcileTask, error)
	ForceReconcile(ctx context.Context, slotID uint32) error
	MarkNodeDraining(ctx context.Context, nodeID uint64) error
	ResumeNode(ctx context.Context, nodeID uint64) error
	TransferSlotLeader(ctx context.Context, slotID uint32, nodeID multiraft.NodeID) error
	RecoverSlot(ctx context.Context, slotID uint32, strategy RecoverStrategy) error
	// RecoverSlotStrict runs slot recovery against controller-leader assignments only.
	RecoverSlotStrict(ctx context.Context, slotID uint32, strategy RecoverStrategy) error
	Rebalance(ctx context.Context) ([]MigrationPlan, error)

	Server() *transport.Server
	RPCMux() *transport.RPCMux
	Discovery() Discovery
	RPCService(ctx context.Context, nodeID multiraft.NodeID, slotID multiraft.SlotID, serviceID uint8, payload []byte) ([]byte, error)
}

var _ API = (*Cluster)(nil)
