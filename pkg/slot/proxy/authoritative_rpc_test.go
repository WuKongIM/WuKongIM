package proxy

import (
	"context"
	"path/filepath"
	"testing"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestHandleRuntimeMetaRPCBeforeClusterStartReturnsNoLeader(t *testing.T) {
	db := openTestDB(t)
	raftDB := openTestRaftDBAt(t, filepath.Join(t.TempDir(), "raft"))

	cluster, err := raftcluster.NewCluster(raftcluster.Config{
		NodeID:             1,
		ListenAddr:         "127.0.0.1:9090",
		SlotCount:          1,
		ControllerReplicaN: 1,
		SlotReplicaN:       1,
		NewStorage: func(slotID multiraft.SlotID) (multiraft.Storage, error) {
			return raftDB.ForSlot(uint64(slotID)), nil
		},
		NewStateMachine:              metafsm.NewStateMachineFactory(db),
		NewStateMachineWithHashSlots: metafsm.NewHashSlotStateMachineFactory(db),
		Nodes: []raftcluster.NodeConfig{{
			NodeID: 1,
			Addr:   "127.0.0.1:9090",
		}},
	})
	require.NoError(t, err)

	store := New(cluster, db)
	body, err := encodeRuntimeMetaRPCRequestBinary(runtimeMetaRPCRequest{
		Op:     runtimeMetaRPCList,
		SlotID: 1,
	})
	require.NoError(t, err)

	var respBody []byte
	require.NotPanics(t, func() {
		respBody, err = store.handleRuntimeMetaRPC(context.Background(), body)
	})
	require.NoError(t, err)

	resp, err := decodeRuntimeMetaRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusNoLeader, resp.Status)
}

func TestRuntimeMetaRPCBinaryCodecRoundTrip(t *testing.T) {
	req := runtimeMetaRPCRequest{
		Op:          runtimeMetaRPCScanPage,
		SlotID:      1,
		ChannelID:   "g1",
		ChannelType: 2,
		After:       &metadb.ChannelRuntimeMetaCursor{ChannelID: "g0", ChannelType: 2},
		Limit:       16,
	}

	reqBody, err := encodeRuntimeMetaRPCRequestBinary(req)
	require.NoError(t, err)
	require.True(t, isRuntimeMetaRPCRequestBinary(reqBody))

	gotReq, err := decodeRuntimeMetaRPCRequest(reqBody)
	require.NoError(t, err)
	require.Equal(t, req.Op, gotReq.Op)
	require.Equal(t, req.SlotID, gotReq.SlotID)
	require.Equal(t, req.ChannelID, gotReq.ChannelID)
	require.Equal(t, req.ChannelType, gotReq.ChannelType)
	require.Equal(t, req.After, gotReq.After)
	require.Equal(t, req.Limit, gotReq.Limit)

	resp := runtimeMetaRPCResponse{
		Status:   rpcStatusOK,
		LeaderID: 3,
		Meta:     &metadb.ChannelRuntimeMeta{ChannelID: "g1", ChannelType: 2, ChannelEpoch: 10, LeaderEpoch: 11, Replicas: []uint64{1, 2}, ISR: []uint64{1}, Leader: 1, MinISR: 1, Status: 1},
		Metas:    []metadb.ChannelRuntimeMeta{{ChannelID: "g2", ChannelType: 2, ChannelEpoch: 20, LeaderEpoch: 21, Replicas: []uint64{2, 1}, ISR: []uint64{2}, Leader: 2, MinISR: 1, Status: 1}},
		Cursor:   metadb.ChannelRuntimeMetaCursor{ChannelID: "g2", ChannelType: 2},
		Done:     true,
	}
	respBody, err := encodeRuntimeMetaRPCResponse(resp)
	require.NoError(t, err)
	require.True(t, isRuntimeMetaRPCResponseBinary(respBody))

	gotResp, err := decodeRuntimeMetaRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp.Status, gotResp.Status)
	require.Equal(t, resp.LeaderID, gotResp.LeaderID)
	require.Equal(t, resp.Meta, gotResp.Meta)
	require.Equal(t, resp.Metas, gotResp.Metas)
	require.Equal(t, resp.Cursor, gotResp.Cursor)
	require.Equal(t, resp.Done, gotResp.Done)
}

func TestRuntimeMetaRPCRejectsJSONPayload(t *testing.T) {
	store := New(nil, openTestDB(t))

	_, err := store.handleRuntimeMetaRPC(context.Background(), []byte(`{"op":"list","slot_id":1}`))
	require.Error(t, err)
}
