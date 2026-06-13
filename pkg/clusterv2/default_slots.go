package clusterv2

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/slots"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/raftlog"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	defaultSlotTickInterval       = 10 * time.Millisecond
	defaultSlotLeaderPollInterval = 10 * time.Millisecond
	defaultSlotElectionTick       = 10
	defaultSlotHeartbeatTick      = 1
	defaultSlotRuntimeWorkerCount = 20
	defaultSlotRaftDirName        = "slotraft"
	defaultSlotMetaDirName        = "slotmeta"
)

// ensureDefaultSlots creates the Slot runtime used by the default proposer.
func (n *Node) ensureDefaultSlots() error {
	if n == nil || n.slots != nil {
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
		Transport:    n.defaultSlotTransport(),
		Observer:     n.cfg.Slots.Observer,
		Goroutines:   n.cfg.Goroutines,
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
	n.registerDefaultSlotHandlers(runtime)
	n.defaultSlotRaftDB = raftDB
	n.defaultSlotMetaDB = metaDB
	n.defaultSlotProposer = defaultSlotProposer{runtime: runtime}
	n.defaultSlots = true
	return nil
}

func (n *Node) defaultSlotTransport() multiraft.Transport {
	if n == nil || n.transportClient == nil {
		return noopSlotTransport{}
	}
	return networkSlotTransport{sender: n.transportClient}
}

func (n *Node) registerDefaultSlotHandlers(runtime *multiraft.Runtime) {
	if n == nil || n.transportServer == nil || runtime == nil {
		return
	}
	n.transportServer.Register(clusternet.MsgSlotRaftBatch, slotRaftBatchHandler{runtime: runtime})
	n.transportServer.Register(clusternet.RPCSlotForwardPropose, propose.NewForwardHandler(defaultSlotProposer{runtime: runtime}))
}

// noopSlotTransport is sufficient for the default single-node Slot runtime.
type noopSlotTransport struct{}

func (noopSlotTransport) Send(context.Context, []multiraft.Envelope) error { return nil }

type networkSlotTransport struct {
	sender clusternet.Sender
}

func (t networkSlotTransport) Send(ctx context.Context, batch []multiraft.Envelope) error {
	if t.sender == nil {
		return nil
	}
	for nodeID, envelopes := range groupSlotRaftEnvelopes(batch) {
		payload, err := encodeSlotRaftBatch(envelopes)
		if err != nil {
			return err
		}
		if err := clusternet.SendOwnedPayload(ctx, t.sender, nodeID, clusternet.MsgSlotRaftBatch, payload); err != nil {
			return err
		}
	}
	return nil
}

func groupSlotRaftEnvelopes(batch []multiraft.Envelope) map[uint64][]multiraft.Envelope {
	byNode := make(map[uint64][]multiraft.Envelope)
	for _, env := range batch {
		if env.Message.To == 0 {
			continue
		}
		nodeID := env.Message.To
		byNode[nodeID] = append(byNode[nodeID], env)
	}
	return byNode
}

type slotRaftBatchHandler struct {
	runtime *multiraft.Runtime
}

func (h slotRaftBatchHandler) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	envelopes, err := decodeSlotRaftBatch(payload)
	if err != nil {
		return nil, err
	}
	for _, env := range envelopes {
		if h.runtime == nil {
			continue
		}
		err := h.runtime.Step(ctx, env)
		if errors.Is(err, multiraft.ErrSlotNotFound) || errors.Is(err, multiraft.ErrSlotClosed) {
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}

type slotRaftEnvelopeDTO struct {
	SlotID  uint64 `json:"slot_id"`
	Message []byte `json:"message"`
}

func encodeSlotRaftBatch(batch []multiraft.Envelope) ([]byte, error) {
	dto := make([]slotRaftEnvelopeDTO, 0, len(batch))
	for _, env := range batch {
		message, err := env.Message.Marshal()
		if err != nil {
			return nil, err
		}
		dto = append(dto, slotRaftEnvelopeDTO{SlotID: uint64(env.SlotID), Message: message})
	}
	return json.Marshal(dto)
}

func decodeSlotRaftBatch(payload []byte) ([]multiraft.Envelope, error) {
	var dto []slotRaftEnvelopeDTO
	if err := json.Unmarshal(payload, &dto); err != nil {
		return nil, err
	}
	out := make([]multiraft.Envelope, 0, len(dto))
	for _, item := range dto {
		var message raftpb.Message
		if err := message.Unmarshal(item.Message); err != nil {
			return nil, err
		}
		out = append(out, multiraft.Envelope{SlotID: multiraft.SlotID(item.SlotID), Message: message})
	}
	return out, nil
}
