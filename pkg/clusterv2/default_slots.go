package clusterv2

import (
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/slots"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/raftlog"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	defaultSlotTickInterval       = 10 * time.Millisecond
	defaultSlotLeaderPollInterval = 10 * time.Millisecond
	defaultSlotElectionTick       = 10
	defaultSlotHeartbeatTick      = 1
	defaultSlotRuntimeWorkerCount = 1
	defaultSlotRaftDirName        = "slotraft"
	defaultSlotMetaDirName        = "slotmeta"
)

// ensureDefaultSlots creates the single-node-cluster Slot runtime used by the default proposer.
func (n *Node) ensureDefaultSlots() error {
	if n == nil || n.slots != nil || !n.defaultSingleNodeSlotsEnabled() {
		return nil
	}
	metaDB, err := metadb.Open(filepath.Join(n.cfg.DataDir, defaultSlotMetaDirName))
	if err != nil {
		return err
	}
	raftDB, err := raftlog.Open(filepath.Join(n.cfg.DataDir, defaultSlotRaftDirName), raftlog.Options{})
	if err != nil {
		_ = metaDB.Close()
		return err
	}
	runtime, err := multiraft.New(multiraft.Options{
		NodeID:       multiraft.NodeID(n.cfg.NodeID),
		TickInterval: defaultSlotTickInterval,
		Workers:      defaultSlotRuntimeWorkerCount,
		Transport:    noopSlotTransport{},
		Raft: multiraft.RaftOptions{
			ElectionTick:  defaultSlotElectionTick,
			HeartbeatTick: defaultSlotHeartbeatTick,
			PreVote:       true,
			CheckQuorum:   true,
		},
	})
	if err != nil {
		_ = raftDB.Close()
		_ = metaDB.Close()
		return err
	}
	adapter := slots.NewAdapter(runtime)
	manager := slots.NewManager(slots.Config{
		LocalNode: n.cfg.NodeID,
		Runtime:   adapter,
		Storage: func(slotID uint32) (multiraft.Storage, error) {
			return raftDB.ForSlot(uint64(slotID)), nil
		},
		StateMachine: func(slotID uint32, hashSlots []uint16) (multiraft.StateMachine, error) {
			return metafsm.NewStateMachineWithHashSlots(metaDB, uint64(slotID), hashSlots)
		},
	})
	n.slots = slots.NewReconciler(n.cfg.NodeID, manager)
	n.defaultSlotRuntime = runtime
	n.defaultSlotRaftDB = raftDB
	n.defaultSlotMetaDB = metaDB
	n.defaultSlotProposer = defaultSlotProposer{runtime: runtime}
	n.defaultSlots = true
	return nil
}

// defaultSingleNodeSlotsEnabled reports whether Node can safely own the default Slot runtime locally.
func (n *Node) defaultSingleNodeSlotsEnabled() bool {
	if n == nil || n.cfg.Slots.ReplicaCount != 1 || len(n.cfg.Control.Voters) != 1 {
		return false
	}
	return n.cfg.Control.Voters[0].NodeID == n.cfg.NodeID
}

// startSlotLeaderLoop publishes local default Slot leadership into the foreground router.
func (n *Node) startSlotLeaderLoop() {
	if n == nil || n.defaultSlotRuntime == nil || n.slotLeaderCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.slotLeaderCancel = cancel
	n.slotLeaderWG.Add(1)
	go func() {
		defer n.slotLeaderWG.Done()
		ticker := time.NewTicker(defaultSlotLeaderPollInterval)
		defer ticker.Stop()
		for {
			n.refreshDefaultSlotLeaders()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// stopSlotLeaderLoop stops the default Slot leadership publisher.
func (n *Node) stopSlotLeaderLoop() {
	if n == nil || n.slotLeaderCancel == nil {
		return
	}
	n.slotLeaderCancel()
	n.slotLeaderWG.Wait()
	n.slotLeaderCancel = nil
}

// refreshDefaultSlotLeaders maps local Multi-Raft status into routing slot leaders.
func (n *Node) refreshDefaultSlotLeaders() {
	if n == nil || n.defaultSlotRuntime == nil || n.router == nil {
		return
	}
	slotIDs := n.currentSlotIDs()
	if len(slotIDs) == 0 {
		return
	}
	n.router.UpdateSlotLeaders(routingSlotStatuses(slots.StatusSnapshot(n.defaultSlotRuntime, slotIDs)))
}

func routingSlotStatuses(statuses []slots.Status) []routing.SlotStatus {
	out := make([]routing.SlotStatus, 0, len(statuses))
	for _, status := range statuses {
		out = append(out, routing.SlotStatus{SlotID: status.SlotID, Leader: status.Leader})
	}
	return out
}

// currentSlotIDs returns the physical Slots visible in the latest local control snapshot.
func (n *Node) currentSlotIDs() []uint32 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]uint32, 0, len(n.controlSnapshot.Slots))
	for _, slot := range n.controlSnapshot.Slots {
		out = append(out, slot.SlotID)
	}
	return out
}

// defaultSlotProposer adapts clusterv2 propose payloads to Multi-Raft Slot proposals.
type defaultSlotProposer struct {
	// runtime is the local single-node Slot Multi-Raft runtime.
	runtime *multiraft.Runtime
}

// IsLocalLeader reports whether the local default Slot runtime leads slotID.
func (p defaultSlotProposer) IsLocalLeader(slotID uint32) bool {
	if p.runtime == nil {
		return false
	}
	status, err := p.runtime.Status(multiraft.SlotID(slotID))
	return err == nil && status.Role == multiraft.RoleLeader
}

// Propose submits one decoded clusterv2 Slot command to the local Multi-Raft runtime.
func (p defaultSlotProposer) Propose(ctx context.Context, slotID uint32, payload []byte) error {
	if p.runtime == nil {
		return propose.ErrInvalidRequest
	}
	if ctx == nil {
		ctx = context.Background()
	}
	hashSlot, command, err := propose.DecodePayload(payload)
	if err != nil {
		return err
	}
	future, err := p.runtime.Propose(ctx, multiraft.SlotID(slotID), multiraftPayload(hashSlot, command))
	if err != nil {
		return mapMultiraftProposeError(err)
	}
	_, err = future.Wait(ctx)
	return mapMultiraftProposeError(err)
}

// multiraftPayload converts clusterv2's propose envelope into Multi-Raft's hash-slot envelope.
func multiraftPayload(hashSlot uint16, command []byte) []byte {
	out := make([]byte, 2+len(command))
	binary.BigEndian.PutUint16(out[:2], hashSlot)
	copy(out[2:], command)
	return out
}

// mapMultiraftProposeError preserves the public propose package error contract.
func mapMultiraftProposeError(err error) error {
	if errors.Is(err, multiraft.ErrNotLeader) {
		return propose.ErrNotLeader
	}
	return err
}

// noopSlotTransport is sufficient for the default single-node Slot runtime.
type noopSlotTransport struct{}

func (noopSlotTransport) Send(context.Context, []multiraft.Envelope) error { return nil }

var _ propose.SlotRuntime = defaultSlotProposer{}
