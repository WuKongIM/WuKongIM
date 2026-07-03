package cluster

import (
	"context"
	"fmt"
	"testing"
	"time"

	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestCompactSlotRaftLogOnNodeRoutesLocalCompaction(t *testing.T) {
	rt, err := multiraft.New(multiraft.Options{
		NodeID:       1,
		TickInterval: 10 * time.Millisecond,
		Workers:      1,
		Transport:    noopManagedSlotTransport{},
		Raft: multiraft.RaftOptions{
			ElectionTick:  10,
			HeartbeatTick: 1,
			LogCompaction: multiraft.LogCompactionConfig{
				Enabled:        true,
				EnabledSet:     true,
				TriggerEntries: 1000,
				CheckInterval:  time.Hour,
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rt.Close()) })

	store := raftstorage.NewMemory()
	slotID := multiraft.SlotID(9)
	require.NoError(t, rt.BootstrapSlot(context.Background(), multiraft.BootstrapSlotRequest{
		Slot: multiraft.SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: &managedSlotSnapshottingStateMachine{},
		},
		Voters: []multiraft.NodeID{1},
	}))
	require.Eventually(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	}, time.Second, 10*time.Millisecond)

	fut, err := rt.Propose(context.Background(), slotID, encodeProposalPayload(0, []byte("compact-me")))
	require.NoError(t, err)
	res, err := fut.Wait(context.Background())
	require.NoError(t, err)

	cluster := &Cluster{
		cfg:     Config{NodeID: 1},
		runtime: rt,
		router:  NewRouter(NewHashSlotTable(16, 1), 1, rt),
	}
	result, err := cluster.CompactSlotRaftLogOnNode(context.Background(), 1, 9)
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.NodeID)
	require.Equal(t, uint32(9), result.SlotID)
	require.True(t, result.Compacted)
	require.Equal(t, res.Index, result.AppliedIndex)
	require.Equal(t, res.Index, result.AfterSnapshotIndex)
}

func TestManagedSlotCompactionCodecRoundTripsResult(t *testing.T) {
	want := SlotRaftCompactionResult{
		NodeID:              2,
		SlotID:              9,
		AppliedIndex:        42,
		BeforeSnapshotIndex: 30,
		AfterSnapshotIndex:  42,
		Compacted:           true,
	}
	body, err := encodeManagedSlotRequest(managedSlotRPCRequest{
		Kind:   managedSlotRPCCompact,
		SlotID: 9,
	})
	require.NoError(t, err)
	req, err := decodeManagedSlotRequest(body)
	require.NoError(t, err)
	require.Equal(t, managedSlotRPCCompact, req.Kind)
	require.Equal(t, uint32(9), req.SlotID)

	respBody, err := encodeManagedSlotResponse(managedSlotRPCResponse{Compaction: &want})
	require.NoError(t, err)
	resp, err := decodeManagedSlotResponse(respBody)
	require.NoError(t, err)
	require.NotNil(t, resp.Compaction)
	require.Equal(t, want, *resp.Compaction)
}

func TestSlotHandlerServesManagedSlotCompaction(t *testing.T) {
	rt, err := multiraft.New(multiraft.Options{
		NodeID:       1,
		TickInterval: 10 * time.Millisecond,
		Workers:      1,
		Transport:    noopManagedSlotTransport{},
		Raft: multiraft.RaftOptions{
			ElectionTick:  10,
			HeartbeatTick: 1,
			LogCompaction: multiraft.LogCompactionConfig{
				Enabled:        true,
				EnabledSet:     true,
				TriggerEntries: 1000,
				CheckInterval:  time.Hour,
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rt.Close()) })

	slotID := multiraft.SlotID(10)
	require.NoError(t, rt.BootstrapSlot(context.Background(), multiraft.BootstrapSlotRequest{
		Slot: multiraft.SlotOptions{
			ID:           slotID,
			Storage:      raftstorage.NewMemory(),
			StateMachine: &managedSlotSnapshottingStateMachine{},
		},
		Voters: []multiraft.NodeID{1},
	}))
	require.Eventually(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	}, time.Second, 10*time.Millisecond)
	fut, err := rt.Propose(context.Background(), slotID, encodeProposalPayload(0, []byte("handler-compact")))
	require.NoError(t, err)
	_, err = fut.Wait(context.Background())
	require.NoError(t, err)

	handler := &slotHandler{cluster: &Cluster{
		cfg:     Config{NodeID: 1},
		runtime: rt,
		router:  NewRouter(NewHashSlotTable(16, 1), 1, rt),
	}}
	body, err := encodeManagedSlotRequest(managedSlotRPCRequest{Kind: managedSlotRPCCompact, SlotID: uint32(slotID)})
	require.NoError(t, err)

	respBody, err := handler.Handle(context.Background(), body)
	require.NoError(t, err)
	resp, err := decodeManagedSlotResponse(respBody)
	require.NoError(t, err)
	require.NotNil(t, resp.Compaction)
	require.Equal(t, uint32(slotID), resp.Compaction.SlotID)
	require.True(t, resp.Compaction.Compacted)
}

type noopManagedSlotTransport struct{}

func (noopManagedSlotTransport) Send(context.Context, []multiraft.Envelope) error { return nil }

type managedSlotSnapshottingStateMachine struct {
	last multiraft.Command
}

func (s *managedSlotSnapshottingStateMachine) Apply(_ context.Context, cmd multiraft.Command) ([]byte, error) {
	s.last = multiraft.Command{
		SlotID:   cmd.SlotID,
		HashSlot: cmd.HashSlot,
		Index:    cmd.Index,
		Term:     cmd.Term,
		Data:     append([]byte(nil), cmd.Data...),
	}
	return []byte("ok"), nil
}

func (s *managedSlotSnapshottingStateMachine) Restore(context.Context, multiraft.Snapshot) error {
	return nil
}

func (s *managedSlotSnapshottingStateMachine) Snapshot(context.Context) (multiraft.Snapshot, error) {
	return multiraft.Snapshot{Data: []byte(fmt.Sprintf("idx=%d", s.last.Index))}, nil
}
