package cluster

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	raft "go.etcd.io/raft/v3"
)

type slotManager struct {
	cluster *Cluster
}

func newSlotManager(cluster *Cluster) *slotManager {
	return &slotManager{cluster: cluster}
}

func (m *slotManager) ensureLocal(ctx context.Context, slotID multiraft.SlotID, desiredPeers []uint64, hasRuntimeView bool, bootstrapAuthorized bool) error {
	if m == nil || m.cluster == nil {
		return ErrNotStarted
	}
	c := m.cluster
	if c.runtime == nil {
		return ErrNotStarted
	}
	if _, err := c.runtime.Status(slotID); err == nil {
		c.setRuntimePeers(slotID, c.runtimePeersForLocalSlot(slotID, desiredPeers))
		return nil
	} else if !errors.Is(err, multiraft.ErrSlotNotFound) {
		return err
	}

	storage, err := c.cfg.NewStorage(slotID)
	if err != nil {
		return err
	}
	sm, err := c.newStateMachine(slotID)
	if err != nil {
		return err
	}
	opts := multiraft.SlotOptions{
		ID:           slotID,
		Storage:      storage,
		StateMachine: sm,
	}

	initialState, err := storage.InitialState(ctx)
	if err != nil {
		return err
	}
	if !raft.IsEmptyHardState(initialState.HardState) {
		peers := nodeIDsFromUint64s(initialState.ConfState.Voters)
		if len(peers) > 0 &&
			!assignmentContainsPeer(desiredPeers, uint64(c.cfg.NodeID)) &&
			!nodeIDsContain(peers, c.cfg.NodeID) {
			c.deleteRuntimePeers(slotID)
			return nil
		}
		if len(peers) == 0 {
			peers = c.runtimePeersForLocalSlot(slotID, desiredPeers)
		}
		c.setRuntimePeers(slotID, peers)
		err := c.runtime.OpenSlot(ctx, opts)
		if err != nil && !errors.Is(err, multiraft.ErrSlotExists) {
			if hook := c.obs.OnSlotEnsure; hook != nil {
				hook(uint32(slotID), "open", err)
			}
			return err
		}
		if hook := c.obs.OnSlotEnsure; hook != nil {
			hook(uint32(slotID), "open", nil)
		}
		return nil
	}

	peers := nodeIDsFromUint64s(desiredPeers)
	if bootstrapAuthorized {
		c.setRuntimePeers(slotID, peers)
		err := c.runtime.BootstrapSlot(ctx, multiraft.BootstrapSlotRequest{
			Slot:   opts,
			Voters: peers,
		})
		if err != nil && !errors.Is(err, multiraft.ErrSlotExists) {
			if hook := c.obs.OnSlotEnsure; hook != nil {
				hook(uint32(slotID), "bootstrap", err)
			}
			return err
		}
		if hook := c.obs.OnSlotEnsure; hook != nil {
			hook(uint32(slotID), "bootstrap", nil)
		}
		return nil
	}
	if !hasRuntimeView {
		return nil
	}
	peers = c.runtimePeersForLocalSlot(slotID, desiredPeers)
	c.setRuntimePeers(slotID, peers)
	err = c.runtime.OpenSlot(ctx, opts)
	if err != nil && !errors.Is(err, multiraft.ErrSlotExists) {
		if hook := c.obs.OnSlotEnsure; hook != nil {
			hook(uint32(slotID), "open", err)
		}
		return err
	}
	if hook := c.obs.OnSlotEnsure; hook != nil {
		hook(uint32(slotID), "open", nil)
	}
	return nil
}

func (m *slotManager) changeConfig(ctx context.Context, slotID multiraft.SlotID, change multiraft.ConfigChange) error {
	if m == nil || m.cluster == nil {
		return ErrNotStarted
	}
	c := m.cluster
	return Retry{
		Interval: c.configChangeRetryInterval(),
		MaxWait:  c.timeoutConfig().ConfigChangeRetryBudget,
		IsRetryable: func(err error) bool {
			return errors.Is(err, ErrNotLeader)
		},
	}.Do(ctx, func(attemptCtx context.Context) error {
		leaderID, err := c.LeaderOf(slotID)
		if err != nil {
			return err
		}
		if c.IsLocal(leaderID) {
			return m.changeConfigLocal(attemptCtx, slotID, change)
		}
		return m.changeConfigRemote(attemptCtx, leaderID, slotID, change)
	})
}

func (m *slotManager) changeConfigLocal(ctx context.Context, slotID multiraft.SlotID, change multiraft.ConfigChange) error {
	if m == nil || m.cluster == nil || m.cluster.runtime == nil {
		return ErrNotStarted
	}
	future, err := m.cluster.runtime.ChangeConfig(ctx, slotID, change)
	if err != nil {
		if errors.Is(err, multiraft.ErrSlotNotFound) {
			return ErrSlotNotFound
		}
		if errors.Is(err, multiraft.ErrNotLeader) {
			return ErrNotLeader
		}
		return err
	}
	_, err = future.Wait(ctx)
	if errors.Is(err, multiraft.ErrNotLeader) {
		return ErrNotLeader
	}
	if errors.Is(err, multiraft.ErrSlotNotFound) {
		return ErrSlotNotFound
	}
	return err
}

func (m *slotManager) changeConfigRemote(ctx context.Context, leaderID multiraft.NodeID, slotID multiraft.SlotID, change multiraft.ConfigChange) error {
	if m == nil || m.cluster == nil {
		return ErrNotStarted
	}
	body, err := encodeManagedSlotRequest(managedSlotRPCRequest{
		Kind:       managedSlotRPCChangeConfig,
		SlotID:     uint32(slotID),
		ChangeType: change.Type,
		NodeID:     uint64(change.NodeID),
	})
	if err != nil {
		return err
	}
	respBody, err := m.cluster.RPCService(ctx, leaderID, slotID, rpcServiceManagedSlot, body)
	if err != nil {
		return err
	}
	_, err = decodeManagedSlotResponse(respBody)
	return err
}

func (m *slotManager) transferLeadership(ctx context.Context, slotID multiraft.SlotID, target multiraft.NodeID) error {
	if m == nil || m.cluster == nil {
		return ErrNotStarted
	}
	c := m.cluster
	return Retry{
		Interval: c.leaderTransferRetryInterval(),
		MaxWait:  c.timeoutConfig().LeaderTransferRetryBudget,
		IsRetryable: func(err error) bool {
			return errors.Is(err, ErrNotLeader)
		},
	}.Do(ctx, func(attemptCtx context.Context) error {
		leaderID, err := c.LeaderOf(slotID)
		if err != nil {
			return err
		}
		if c.IsLocal(leaderID) {
			return m.transferLeaderLocal(attemptCtx, slotID, target)
		}
		return m.transferLeaderRemote(attemptCtx, leaderID, slotID, target)
	})
}

func (m *slotManager) transferLeaderLocal(ctx context.Context, slotID multiraft.SlotID, target multiraft.NodeID) error {
	if m == nil || m.cluster == nil || m.cluster.runtime == nil {
		return ErrNotStarted
	}
	err := m.cluster.runtime.TransferLeadership(ctx, slotID, target)
	if errors.Is(err, multiraft.ErrSlotNotFound) {
		return ErrSlotNotFound
	}
	return err
}

func (m *slotManager) transferLeaderRemote(ctx context.Context, leaderID multiraft.NodeID, slotID multiraft.SlotID, target multiraft.NodeID) error {
	if m == nil || m.cluster == nil {
		return ErrNotStarted
	}
	body, err := encodeManagedSlotRequest(managedSlotRPCRequest{
		Kind:       managedSlotRPCTransferLeader,
		SlotID:     uint32(slotID),
		TargetNode: uint64(target),
	})
	if err != nil {
		return err
	}
	respBody, err := m.cluster.RPCService(ctx, leaderID, slotID, rpcServiceManagedSlot, body)
	if err != nil {
		return err
	}
	_, err = decodeManagedSlotResponse(respBody)
	return err
}

func (m *slotManager) waitForLeader(ctx context.Context, slotID multiraft.SlotID) error {
	if m == nil || m.cluster == nil {
		return ErrNotStarted
	}
	c := m.cluster
	deadline := time.Now().Add(c.timeoutConfig().ManagedSlotLeaderWait)
	var lastLeader multiraft.NodeID
	for {
		if time.Now().After(deadline) {
			return context.DeadlineExceeded
		}
		leaderID, err := m.currentLeader(slotID)
		if err == nil && leaderID != 0 {
			if hook := c.obs.OnLeaderChange; hook != nil && leaderID != lastLeader {
				hook(uint32(slotID), lastLeader, leaderID)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.managedSlotLeaderPollInterval()):
		}
	}
}

func (m *slotManager) waitForCatchUp(ctx context.Context, slotID multiraft.SlotID, targetNode multiraft.NodeID) error {
	if m == nil || m.cluster == nil {
		return ErrNotStarted
	}
	c := m.cluster
	deadline := time.Now().Add(c.timeoutConfig().ManagedSlotCatchUp)
	for {
		if time.Now().After(deadline) {
			return context.DeadlineExceeded
		}
		targetStatus, err := m.statusOnNode(ctx, targetNode, slotID)
		if err == nil {
			leaderID, leaderErr := m.currentLeader(slotID)
			if leaderErr == nil && leaderID != 0 {
				leaderStatus, statusErr := m.statusOnNode(ctx, leaderID, slotID)
				if statusErr == nil && targetStatus.AppliedIndex >= leaderStatus.CommitIndex {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.managedSlotCatchUpPollInterval()):
		}
	}
}

func (m *slotManager) currentLeader(slotID multiraft.SlotID) (multiraft.NodeID, error) {
	if m == nil || m.cluster == nil {
		return 0, ErrNotStarted
	}
	c := m.cluster
	c.managedSlotHooks.mu.RLock()
	hook := c.managedSlotHooks.leader
	c.managedSlotHooks.mu.RUnlock()
	if hook != nil {
		if leaderID, err, handled := hook(c, slotID); handled {
			return leaderID, err
		}
	}
	return c.LeaderOf(slotID)
}

func (m *slotManager) ensureLeaderMovedOffSource(ctx context.Context, slotID multiraft.SlotID, sourceNode, targetNode multiraft.NodeID) error {
	if m == nil || m.cluster == nil {
		return ErrNotStarted
	}
	if sourceNode == 0 || targetNode == 0 {
		return nil
	}
	c := m.cluster
	deadline := time.Now().Add(c.timeoutConfig().ManagedSlotLeaderMove)
	for {
		if time.Now().After(deadline) {
			return ErrLeaderNotStable
		}
		leaderID, err := m.currentLeader(slotID)
		if err != nil || leaderID == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.managedSlotLeaderMovePollInterval()):
			}
			continue
		}
		if leaderID != sourceNode {
			if hook := c.obs.OnLeaderChange; hook != nil {
				hook(uint32(slotID), sourceNode, leaderID)
			}
			return nil
		}
		if err := m.transferLeadership(ctx, slotID, targetNode); err != nil && !errors.Is(err, ErrNotLeader) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.managedSlotLeaderMovePollInterval()):
		}
	}
}

func (m *slotManager) statusOnNode(ctx context.Context, nodeID multiraft.NodeID, slotID multiraft.SlotID) (managedSlotStatus, error) {
	if m == nil || m.cluster == nil {
		return managedSlotStatus{}, ErrNotStarted
	}
	c := m.cluster
	c.managedSlotHooks.mu.RLock()
	hook := c.managedSlotHooks.status
	c.managedSlotHooks.mu.RUnlock()
	if hook != nil {
		if status, err, handled := hook(c, nodeID, slotID); handled {
			return status, err
		}
	}

	if c.IsLocal(nodeID) {
		return m.localStatus(slotID)
	}

	body, err := encodeManagedSlotRequest(managedSlotRPCRequest{
		Kind:   managedSlotRPCStatus,
		SlotID: uint32(slotID),
	})
	if err != nil {
		return managedSlotStatus{}, err
	}
	respBody, err := c.RPCService(ctx, nodeID, slotID, rpcServiceManagedSlot, body)
	if err != nil {
		return managedSlotStatus{}, err
	}
	resp, err := decodeManagedSlotResponse(respBody)
	if err != nil {
		return managedSlotStatus{}, err
	}
	return managedSlotStatus{
		LeaderID:     multiraft.NodeID(resp.LeaderID),
		CommitIndex:  resp.CommitIndex,
		AppliedIndex: resp.AppliedIndex,
	}, nil
}

func (m *slotManager) localStatus(slotID multiraft.SlotID) (managedSlotStatus, error) {
	if m == nil || m.cluster == nil || m.cluster.runtime == nil {
		return managedSlotStatus{}, ErrNotStarted
	}
	status, err := m.cluster.runtime.Status(slotID)
	if err != nil {
		if errors.Is(err, multiraft.ErrSlotNotFound) {
			return managedSlotStatus{}, ErrSlotNotFound
		}
		return managedSlotStatus{}, err
	}
	return managedSlotStatus{
		LeaderID:     status.LeaderID,
		CommitIndex:  status.CommitIndex,
		AppliedIndex: status.AppliedIndex,
	}, nil
}
