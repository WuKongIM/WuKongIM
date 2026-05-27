package clusterv2

import (
	"context"
	"path/filepath"
	"time"

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

// noopSlotTransport is sufficient for the default single-node Slot runtime.
type noopSlotTransport struct{}

func (noopSlotTransport) Send(context.Context, []multiraft.Envelope) error { return nil }
