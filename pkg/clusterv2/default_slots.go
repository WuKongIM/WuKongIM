package clusterv2

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/slots"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/tasks"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/raftlog"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	defaultSlotTickInterval       = 10 * time.Millisecond
	defaultSlotLeaderPollInterval = 10 * time.Millisecond
	defaultSlotElectionTick       = 50
	defaultSlotHeartbeatTick      = 1
	defaultSlotRuntimeWorkerCount = 20
	defaultSlotRaftDirName        = "slotraft"
	defaultSlotMetaDirName        = "slotmeta"
	slotRaftBatchMagic            = "WKSRB1"
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
		TickInterval: n.cfg.Slots.TickInterval,
		Workers:      defaultSlotRuntimeWorkerCount,
		Transport:    n.defaultSlotTransport(),
		Observer:     n.cfg.Slots.Observer,
		Goroutines:   n.cfg.Goroutines,
		Raft: multiraft.RaftOptions{
			ElectionTick:  n.cfg.Slots.ElectionTick,
			HeartbeatTick: n.cfg.Slots.HeartbeatTick,
			PreVote:       true,
			CheckQuorum:   true,
			LogCompaction: n.cfg.Slots.LogCompaction,
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
	if n.tasks == nil && n.control != nil {
		n.tasks = tasks.NewCompositeExecutor(
			tasks.NewBootstrapExecutor(tasks.BootstrapExecutorConfig{
				LocalNode: n.cfg.NodeID,
				Slots:     manager,
				Status:    runtime,
				Writer:    n.control,
			}),
			tasks.NewLeaderTransferExecutor(tasks.LeaderTransferExecutorConfig{
				LocalNode: n.cfg.NodeID,
				Runtime:   runtime,
				Writer:    n.control,
			}),
			tasks.NewSlotReplicaMoveExecutor(tasks.SlotReplicaMoveExecutorConfig{
				LocalNode:  n.cfg.NodeID,
				Runtime:    runtime,
				Learners:   manager,
				MoveWriter: n.control,
				Observer:   n.cfg.Slots.ReplicaMoveObserver,
			}),
		)
	}
	n.defaultSlotRuntime = runtime
	n.defaultSlotRaftDB = raftDB
	n.defaultSlotMetaDB = metaDB
	n.defaultSlotProposer = defaultSlotProposer{runtime: runtime}
	n.registerDefaultSlotHandlers(runtime)
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
	n.transportServer.Register(clusternet.RPCPluginBindingScan, pluginBindingScanHandler{node: n})
	n.transportServer.Register(clusternet.RPCSlotStatus, slotStatusHandler{runtime: runtime})
	n.transportServer.Register(clusternet.RPCChannelMigrationMeta, channelMigrationMetaHandler{node: n})
}

// noopSlotTransport is sufficient for the default single-node Slot runtime.
type noopSlotTransport struct{}

func (noopSlotTransport) Send(context.Context, []multiraft.Envelope) error { return nil }

type networkSlotTransport struct {
	sender clusternet.Sender
}

func (networkSlotTransport) OwnsReadyMessagePayloads() bool { return true }

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

func encodeSlotRaftBatch(batch []multiraft.Envelope) ([]byte, error) {
	messages := make([][]byte, len(batch))
	size := len(slotRaftBatchMagic) + binary.MaxVarintLen64
	for i, env := range batch {
		message, err := env.Message.Marshal()
		if err != nil {
			return nil, err
		}
		messages[i] = message
		size += binary.MaxVarintLen64 + binary.MaxVarintLen64 + len(message)
	}
	payload := make([]byte, 0, size)
	payload = append(payload, slotRaftBatchMagic...)
	payload = appendSlotRaftBatchUvarint(payload, uint64(len(batch)))
	for i, env := range batch {
		payload = appendSlotRaftBatchUvarint(payload, uint64(env.SlotID))
		payload = appendSlotRaftBatchUvarint(payload, uint64(len(messages[i])))
		payload = append(payload, messages[i]...)
	}
	return payload, nil
}

func decodeSlotRaftBatch(payload []byte) ([]multiraft.Envelope, error) {
	if !bytes.HasPrefix(payload, []byte(slotRaftBatchMagic)) {
		return nil, fmt.Errorf("clusterv2: invalid Slot Raft batch frame")
	}
	pos := len(slotRaftBatchMagic)
	count, err := readSlotRaftBatchUvarint(payload, &pos)
	if err != nil {
		return nil, err
	}
	if count > uint64(len(payload)-pos)/2 {
		return nil, fmt.Errorf("clusterv2: Slot Raft batch envelope count %d exceeds payload size", count)
	}
	out := make([]multiraft.Envelope, 0, int(count))
	for i := uint64(0); i < count; i++ {
		slotID, err := readSlotRaftBatchUvarint(payload, &pos)
		if err != nil {
			return nil, err
		}
		messageLen, err := readSlotRaftBatchUvarint(payload, &pos)
		if err != nil {
			return nil, err
		}
		if messageLen > uint64(len(payload)-pos) {
			return nil, fmt.Errorf("clusterv2: truncated Slot Raft message payload")
		}
		messageEnd := pos + int(messageLen)
		var message raftpb.Message
		if err := message.Unmarshal(payload[pos:messageEnd]); err != nil {
			return nil, err
		}
		pos = messageEnd
		out = append(out, multiraft.Envelope{SlotID: multiraft.SlotID(slotID), Message: message})
	}
	if pos != len(payload) {
		return nil, fmt.Errorf("clusterv2: trailing bytes in Slot Raft batch frame")
	}
	return out, nil
}

func appendSlotRaftBatchUvarint(dst []byte, value uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], value)
	return append(dst, buf[:n]...)
}

func readSlotRaftBatchUvarint(payload []byte, pos *int) (uint64, error) {
	if pos == nil || *pos > len(payload) {
		return 0, fmt.Errorf("clusterv2: invalid Slot Raft batch cursor")
	}
	value, n := binary.Uvarint(payload[*pos:])
	switch {
	case n > 0:
		*pos += n
		return value, nil
	case n == 0:
		return 0, fmt.Errorf("clusterv2: truncated Slot Raft batch varint")
	default:
		return 0, fmt.Errorf("clusterv2: oversized Slot Raft batch varint")
	}
}
