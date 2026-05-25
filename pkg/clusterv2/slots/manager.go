package slots

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	raft "go.etcd.io/raft/v3"
)

// Manager opens or bootstraps local Slot runtimes according to assignments.
type Manager struct {
	localNode  uint64
	runtime    Runtime
	storage    StorageFactory
	stateMach  StateMachineFactory
	unassigned map[uint32]struct{}
}

// NewManager creates a Manager from cfg.
func NewManager(cfg Config) *Manager {
	return &Manager{localNode: cfg.LocalNode, runtime: cfg.Runtime, storage: cfg.Storage, stateMach: cfg.StateMachine, unassigned: make(map[uint32]struct{})}
}

// BootstrapOwner returns the deterministic node allowed to bootstrap assignment.
func BootstrapOwner(assignment Assignment) uint64 {
	if assignment.PreferredLeader != 0 {
		return assignment.PreferredLeader
	}
	if len(assignment.DesiredPeers) == 0 {
		return 0
	}
	peers := append([]uint64(nil), assignment.DesiredPeers...)
	sort.Slice(peers, func(i, j int) bool { return peers[i] < peers[j] })
	return peers[0]
}

// Ensure opens or bootstraps the local Slot state for assignment.
func (m *Manager) Ensure(ctx context.Context, assignment Assignment) error {
	if m == nil || m.runtime == nil || m.storage == nil || m.stateMach == nil {
		return fmt.Errorf("slots: manager not configured")
	}
	if assignment.SlotID == 0 {
		return fmt.Errorf("slots: slot id must be > 0")
	}
	if _, err := m.runtime.Status(multiraft.SlotID(assignment.SlotID)); err == nil {
		return nil
	} else if err != nil && !errors.Is(err, multiraft.ErrSlotNotFound) {
		return err
	}
	if !containsNode(assignment.DesiredPeers, m.localNode) {
		m.unassigned[assignment.SlotID] = struct{}{}
		return nil
	}
	storage, err := m.storage(assignment.SlotID)
	if err != nil {
		return err
	}
	stateMachine, err := m.stateMach(assignment.SlotID, assignment.HashSlots)
	if err != nil {
		return err
	}
	opts := multiraft.SlotOptions{ID: multiraft.SlotID(assignment.SlotID), Storage: storage, StateMachine: stateMachine}
	initial, err := storage.InitialState(ctx)
	if err != nil {
		return err
	}
	if !raft.IsEmptyHardState(initial.HardState) {
		return m.runtime.OpenSlot(ctx, opts)
	}
	if BootstrapOwner(assignment) == m.localNode {
		return m.runtime.BootstrapSlot(ctx, multiraft.BootstrapSlotRequest{Slot: opts, Voters: nodeIDs(assignment.DesiredPeers)})
	}
	if assignment.Bootstrapped {
		return m.runtime.OpenSlot(ctx, opts)
	}
	return nil
}

// IsUnassigned reports whether Ensure observed slotID as not assigned to this node.
func (m *Manager) IsUnassigned(slotID uint32) bool {
	if m == nil {
		return false
	}
	_, ok := m.unassigned[slotID]
	return ok
}

func containsNode(peers []uint64, nodeID uint64) bool {
	for _, peer := range peers {
		if peer == nodeID {
			return true
		}
	}
	return false
}

func nodeIDs(peers []uint64) []multiraft.NodeID {
	out := make([]multiraft.NodeID, 0, len(peers))
	for _, peer := range peers {
		out = append(out, multiraft.NodeID(peer))
	}
	return out
}
