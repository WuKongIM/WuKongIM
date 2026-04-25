package cluster

import (
	"context"
	"errors"
	"sync"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	rpcServiceManagedSlot        uint8  = 20
	managedSlotRPCStatus         string = "status"
	managedSlotRPCChangeConfig   string = "change_config"
	managedSlotRPCImportSnapshot string = "import_snapshot"
	managedSlotRPCTransferLeader string = "transfer_leader"
)

type managedSlotRPCRequest struct {
	Kind       string               `json:"kind"`
	SlotID     uint32               `json:"slot_id"`
	TargetNode uint64               `json:"target_node,omitempty"`
	ChangeType multiraft.ChangeType `json:"change_type,omitempty"`
	NodeID     uint64               `json:"node_id,omitempty"`
	HashSlot   uint16               `json:"hash_slot,omitempty"`
	Snapshot   []byte               `json:"snapshot,omitempty"`
}

type managedSlotRPCResponse struct {
	NotLeader    bool   `json:"not_leader,omitempty"`
	NotFound     bool   `json:"not_found,omitempty"`
	Timeout      bool   `json:"timeout,omitempty"`
	Message      string `json:"message,omitempty"`
	LeaderID     uint64 `json:"leader_id,omitempty"`
	CommitIndex  uint64 `json:"commit_index,omitempty"`
	AppliedIndex uint64 `json:"applied_index,omitempty"`
}

type ManagedSlotExecutionTestHook func(slotID uint32, task controllermeta.ReconcileTask) error

type managedSlotStatus struct {
	LeaderID     multiraft.NodeID
	CommitIndex  uint64
	AppliedIndex uint64
}

type managedSlotStatusTestHook func(c *Cluster, nodeID multiraft.NodeID, slotID multiraft.SlotID) (managedSlotStatus, error, bool)
type managedSlotLeaderTestHook func(c *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool)

type managedSlotHooks struct {
	mu        sync.RWMutex
	execution ManagedSlotExecutionTestHook
	status    managedSlotStatusTestHook
	leader    managedSlotLeaderTestHook
}

func (c *Cluster) SetManagedSlotExecutionTestHook(hook ManagedSlotExecutionTestHook) func() {
	if c == nil {
		return func() {}
	}
	c.managedSlotHooks.mu.Lock()
	prev := c.managedSlotHooks.execution
	c.managedSlotHooks.execution = hook
	c.managedSlotHooks.mu.Unlock()
	return func() {
		c.managedSlotHooks.mu.Lock()
		c.managedSlotHooks.execution = prev
		c.managedSlotHooks.mu.Unlock()
	}
}

func (c *Cluster) setManagedSlotStatusTestHook(hook managedSlotStatusTestHook) func() {
	if c == nil {
		return func() {}
	}
	c.managedSlotHooks.mu.Lock()
	prev := c.managedSlotHooks.status
	c.managedSlotHooks.status = hook
	c.managedSlotHooks.mu.Unlock()
	return func() {
		c.managedSlotHooks.mu.Lock()
		c.managedSlotHooks.status = prev
		c.managedSlotHooks.mu.Unlock()
	}
}

func (c *Cluster) setManagedSlotLeaderTestHook(hook managedSlotLeaderTestHook) func() {
	if c == nil {
		return func() {}
	}
	c.managedSlotHooks.mu.Lock()
	prev := c.managedSlotHooks.leader
	c.managedSlotHooks.leader = hook
	c.managedSlotHooks.mu.Unlock()
	return func() {
		c.managedSlotHooks.mu.Lock()
		c.managedSlotHooks.leader = prev
		c.managedSlotHooks.mu.Unlock()
	}
}

func (c *Cluster) handleManagedSlotRPC(ctx context.Context, body []byte) ([]byte, error) {
	return (&slotHandler{cluster: c}).Handle(ctx, body)
}

func (c *Cluster) slotExecution() *slotExecutor {
	if c == nil {
		return nil
	}
	if c.slotExecutor == nil {
		c.slotExecutor = newSlotExecutor(c)
	}
	return c.slotExecutor
}

func (c *Cluster) managedSlots() *slotManager {
	if c == nil {
		return nil
	}
	if c.slotMgr == nil {
		c.slotMgr = newSlotManager(c)
	}
	return c.slotMgr
}

func marshalManagedSlotError(err error) ([]byte, error) {
	switch {
	case err == nil:
		return encodeManagedSlotResponse(managedSlotRPCResponse{})
	case errors.Is(err, ErrNotLeader):
		return encodeManagedSlotResponse(managedSlotRPCResponse{NotLeader: true})
	case errors.Is(err, ErrSlotNotFound), errors.Is(err, multiraft.ErrSlotNotFound):
		return encodeManagedSlotResponse(managedSlotRPCResponse{NotFound: true})
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return encodeManagedSlotResponse(managedSlotRPCResponse{Timeout: true})
	default:
		return encodeManagedSlotResponse(managedSlotRPCResponse{Message: err.Error()})
	}
}

func (c *Cluster) ensureManagedSlotLocal(ctx context.Context, slotID multiraft.SlotID, desiredPeers []uint64, hasRuntimeView bool, bootstrapAuthorized bool) error {
	return c.managedSlots().ensureLocal(ctx, slotID, desiredPeers, hasRuntimeView, bootstrapAuthorized)
}

func (c *Cluster) executeReconcileTask(ctx context.Context, assignment assignmentTaskState) (err error) {
	executor := c.slotExecution()
	if executor == nil {
		return ErrNotStarted
	}
	return executor.Execute(ctx, assignment)
}

func (c *Cluster) changeSlotConfig(ctx context.Context, slotID multiraft.SlotID, change multiraft.ConfigChange) error {
	return c.managedSlots().changeConfig(ctx, slotID, change)
}

func (c *Cluster) changeSlotConfigLocal(ctx context.Context, slotID multiraft.SlotID, change multiraft.ConfigChange) error {
	return c.managedSlots().changeConfigLocal(ctx, slotID, change)
}

func (c *Cluster) changeSlotConfigRemote(ctx context.Context, leaderID multiraft.NodeID, slotID multiraft.SlotID, change multiraft.ConfigChange) error {
	return c.managedSlots().changeConfigRemote(ctx, leaderID, slotID, change)
}

func (c *Cluster) transferSlotLeadership(ctx context.Context, slotID multiraft.SlotID, target multiraft.NodeID) error {
	return c.managedSlots().transferLeadership(ctx, slotID, target)
}

func (c *Cluster) transferSlotLeaderLocal(ctx context.Context, slotID multiraft.SlotID, target multiraft.NodeID) error {
	return c.managedSlots().transferLeaderLocal(ctx, slotID, target)
}

func (c *Cluster) transferSlotLeaderRemote(ctx context.Context, leaderID multiraft.NodeID, slotID multiraft.SlotID, target multiraft.NodeID) error {
	return c.managedSlots().transferLeaderRemote(ctx, leaderID, slotID, target)
}

func (c *Cluster) waitForManagedSlotLeader(ctx context.Context, slotID multiraft.SlotID) error {
	return c.managedSlots().waitForLeader(ctx, slotID)
}

func (c *Cluster) waitForManagedSlotCatchUp(ctx context.Context, slotID multiraft.SlotID, targetNode multiraft.NodeID) error {
	return c.managedSlots().waitForCatchUp(ctx, slotID, targetNode)
}

func (c *Cluster) currentManagedSlotLeader(slotID multiraft.SlotID) (multiraft.NodeID, error) {
	return c.managedSlots().currentLeader(slotID)
}

func (c *Cluster) ensureLeaderMovedOffSource(ctx context.Context, slotID multiraft.SlotID, sourceNode, targetNode multiraft.NodeID) error {
	return c.managedSlots().ensureLeaderMovedOffSource(ctx, slotID, sourceNode, targetNode)
}

func (c *Cluster) managedSlotStatusOnNode(ctx context.Context, nodeID multiraft.NodeID, slotID multiraft.SlotID) (managedSlotStatus, error) {
	return c.managedSlots().statusOnNode(ctx, nodeID, slotID)
}

func (c *Cluster) localManagedSlotStatus(slotID multiraft.SlotID) (managedSlotStatus, error) {
	return c.managedSlots().localStatus(slotID)
}

func reconcileStepName(task controllermeta.ReconcileTask) string {
	if name := taskStepName(task.Step); name != "" {
		return name
	}
	switch task.Kind {
	case controllermeta.TaskKindBootstrap:
		return "bootstrap"
	case controllermeta.TaskKindRepair:
		return "repair"
	case controllermeta.TaskKindRebalance:
		return "rebalance"
	default:
		return "unknown"
	}
}

func taskStepName(step controllermeta.TaskStep) string {
	switch step {
	case controllermeta.TaskStepAddLearner:
		return "add_learner"
	case controllermeta.TaskStepCatchUp:
		return "catch_up"
	case controllermeta.TaskStepPromote:
		return "promote"
	case controllermeta.TaskStepTransferLeader:
		return "transfer_leader"
	case controllermeta.TaskStepRemoveOld:
		return "remove_old"
	default:
		return ""
	}
}

func cloneSlotSnapshot(snap metadb.SlotSnapshot) metadb.SlotSnapshot {
	return metadb.SlotSnapshot{
		HashSlots: append([]uint16(nil), snap.HashSlots...),
		Data:      append([]byte(nil), snap.Data...),
		Stats:     snap.Stats,
	}
}

func (c *Cluster) exportHashSlotSnapshot(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16) (metadb.SlotSnapshot, uint64, error) {
	sm, ok := c.runtimeStateMachine(slotID)
	if !ok {
		return metadb.SlotSnapshot{}, 0, ErrSlotNotFound
	}
	exporter, ok := sm.(hashSlotSnapshotExporter)
	if !ok {
		return metadb.SlotSnapshot{}, 0, ErrInvalidConfig
	}

	status, err := c.managedSlotStatusOnNode(ctx, c.cfg.NodeID, slotID)
	if err != nil {
		return metadb.SlotSnapshot{}, 0, err
	}

	snap, err := exporter.ExportHashSlotSnapshot(ctx, hashSlot)
	if err != nil {
		return metadb.SlotSnapshot{}, 0, err
	}
	return cloneSlotSnapshot(snap), status.AppliedIndex, nil
}

func (c *Cluster) importHashSlotSnapshot(ctx context.Context, slotID multiraft.SlotID, snap metadb.SlotSnapshot) error {
	leaderID, err := c.currentManagedSlotLeader(slotID)
	if err != nil {
		return err
	}
	if c.IsLocal(leaderID) {
		return c.importHashSlotSnapshotLocal(ctx, slotID, snap)
	}
	return c.importHashSlotSnapshotRemote(ctx, leaderID, slotID, snap)
}

func (c *Cluster) importHashSlotSnapshotLocal(ctx context.Context, slotID multiraft.SlotID, snap metadb.SlotSnapshot) error {
	sm, ok := c.runtimeStateMachine(slotID)
	if !ok {
		return ErrSlotNotFound
	}
	importer, ok := sm.(hashSlotSnapshotImporter)
	if !ok {
		return ErrInvalidConfig
	}
	return importer.ImportHashSlotSnapshot(ctx, cloneSlotSnapshot(snap))
}

func (c *Cluster) importHashSlotSnapshotRemote(ctx context.Context, leaderID multiraft.NodeID, slotID multiraft.SlotID, snap metadb.SlotSnapshot) error {
	if len(snap.HashSlots) != 1 {
		return ErrInvalidConfig
	}
	body, err := encodeManagedSlotRequest(managedSlotRPCRequest{
		Kind:     managedSlotRPCImportSnapshot,
		SlotID:   uint32(slotID),
		HashSlot: snap.HashSlots[0],
		Snapshot: append([]byte(nil), snap.Data...),
	})
	if err != nil {
		return err
	}
	respBody, err := c.RPCService(ctx, leaderID, slotID, rpcServiceManagedSlot, body)
	if err != nil {
		return err
	}
	_, err = decodeManagedSlotResponse(respBody)
	return err
}

func assignmentContainsPeer(peers []uint64, nodeID uint64) bool {
	for _, peer := range peers {
		if peer == nodeID {
			return true
		}
	}
	return false
}

func nodeIDsContain(ids []multiraft.NodeID, nodeID multiraft.NodeID) bool {
	for _, id := range ids {
		if id == nodeID {
			return true
		}
	}
	return false
}

func (c *Cluster) runtimePeersForLocalSlot(slotID multiraft.SlotID, desiredPeers []uint64) []multiraft.NodeID {
	peers := nodeIDsFromUint64s(desiredPeers)
	if c == nil {
		return peers
	}
	if assignmentContainsPeer(desiredPeers, uint64(c.cfg.NodeID)) {
		return peers
	}
	currentPeers, ok := c.getRuntimePeers(slotID)
	if ok && nodeIDsContain(currentPeers, c.cfg.NodeID) {
		return currentPeers
	}
	return peers
}

func reconcileTaskRunnable(now time.Time, task controllermeta.ReconcileTask) bool {
	switch task.Status {
	case controllermeta.TaskStatusPending:
		return true
	case controllermeta.TaskStatusRetrying:
		return !task.NextRunAt.After(now)
	default:
		return false
	}
}
