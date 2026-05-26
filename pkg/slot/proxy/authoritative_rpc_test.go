package proxy

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
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
	require.True(t, runtimeMetaHasMagic(respBody, runtimeMetaRPCResponseMagic[:]))

	resp, err := decodeRuntimeMetaRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusNoLeader, resp.Status)
}

func TestHandleRuntimeMetaRPCLegacyRequestEmitsLegacyResponse(t *testing.T) {
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
		Op:           runtimeMetaRPCList,
		SlotID:       1,
		CodecVersion: 1,
	})
	require.NoError(t, err)

	respBody, err := store.handleRuntimeMetaRPC(context.Background(), body)
	require.NoError(t, err)
	require.True(t, runtimeMetaHasMagic(respBody, runtimeMetaRPCResponseMagicV1[:]))

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
	require.Equal(t, byte(3), gotReq.CodecVersion)

	resp := runtimeMetaRPCResponse{
		Status:   rpcStatusOK,
		LeaderID: 3,
		Meta:     &metadb.ChannelRuntimeMeta{ChannelID: "g1", ChannelType: 2, ChannelEpoch: 10, LeaderEpoch: 11, Replicas: []uint64{1, 2}, ISR: []uint64{1}, Leader: 1, MinISR: 1, Status: 1, WriteFenceToken: "task-1", WriteFenceVersion: 7, WriteFenceReason: 2, WriteFenceUntilMS: 1710000000000, RouteGeneration: 42},
		Metas:    []metadb.ChannelRuntimeMeta{{ChannelID: "g2", ChannelType: 2, ChannelEpoch: 20, LeaderEpoch: 21, Replicas: []uint64{2, 1}, ISR: []uint64{2}, Leader: 2, MinISR: 1, Status: 1, WriteFenceVersion: 8, RouteGeneration: 43}},
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
	require.Equal(t, metadb.NormalizeChannelRuntimeMeta(*resp.Meta), *gotResp.Meta)
	require.Equal(t, []metadb.ChannelRuntimeMeta{metadb.NormalizeChannelRuntimeMeta(resp.Metas[0])}, gotResp.Metas)
	require.Equal(t, resp.Cursor, gotResp.Cursor)
	require.Equal(t, resp.Done, gotResp.Done)
}

func TestRuntimeMetaRPCBinaryCodecDefaultsLegacyRouteGeneration(t *testing.T) {
	resp := runtimeMetaRPCResponse{
		Status: rpcStatusOK,
		Meta:   &metadb.ChannelRuntimeMeta{ChannelID: "g1", ChannelType: 2, ChannelEpoch: 10, LeaderEpoch: 11, Replicas: []uint64{1, 2}, ISR: []uint64{1}, Leader: 1, MinISR: 1, Status: 1},
	}
	body, err := encodeRuntimeMetaRPCResponseForVersion(resp, 2)
	require.NoError(t, err)

	got, err := decodeRuntimeMetaRPCResponse(body)
	require.NoError(t, err)
	require.Equal(t, uint64(11), got.Meta.RouteGeneration)
}

func TestRuntimeMetaRPCBinaryCodecDecodesLegacyResponse(t *testing.T) {
	resp := runtimeMetaRPCResponse{
		Status:   rpcStatusOK,
		LeaderID: 3,
		Meta:     &metadb.ChannelRuntimeMeta{ChannelID: "g1", ChannelType: 2, ChannelEpoch: 10, LeaderEpoch: 11, Replicas: []uint64{1, 2}, ISR: []uint64{1}, Leader: 1, MinISR: 1, Status: 1},
		Metas:    []metadb.ChannelRuntimeMeta{{ChannelID: "g2", ChannelType: 2, ChannelEpoch: 20, LeaderEpoch: 21, Replicas: []uint64{2, 1}, ISR: []uint64{2}, Leader: 2, MinISR: 1, Status: 1}},
		Cursor:   metadb.ChannelRuntimeMetaCursor{ChannelID: "g2", ChannelType: 2},
		Done:     true,
	}
	body, err := encodeRuntimeMetaRPCResponseForVersion(resp, 1)
	require.NoError(t, err)

	got, err := decodeRuntimeMetaRPCResponse(body)
	require.NoError(t, err)
	require.Equal(t, uint64(11), got.Meta.RouteGeneration)
	require.Equal(t, uint64(21), got.Metas[0].RouteGeneration)
	require.Zero(t, got.Meta.WriteFenceVersion)
	require.Zero(t, got.Metas[0].WriteFenceVersion)
}

func TestRuntimeMetaRPCBinaryCodecRejectsTruncatedWriteFenceTail(t *testing.T) {
	resp := runtimeMetaRPCResponse{
		Status: rpcStatusOK,
		Meta:   &metadb.ChannelRuntimeMeta{ChannelID: "g1", ChannelType: 2, ChannelEpoch: 10, LeaderEpoch: 11, Replicas: []uint64{1, 2}, ISR: []uint64{1}, Leader: 1, MinISR: 1, Status: 1, WriteFenceToken: "task-1", WriteFenceVersion: 7, WriteFenceReason: 1, WriteFenceUntilMS: 1710000000000},
	}
	body, err := encodeRuntimeMetaRPCResponse(resp)
	require.NoError(t, err)
	require.NotEmpty(t, body)

	_, err = decodeRuntimeMetaRPCResponse(body[:len(body)-1])
	require.Error(t, err)
}

func TestRuntimeMetaRPCBinaryCodecLegacyRequestRoundTrip(t *testing.T) {
	req := runtimeMetaRPCRequest{
		Op:           runtimeMetaRPCList,
		SlotID:       1,
		CodecVersion: 1,
	}

	body, err := encodeRuntimeMetaRPCRequestBinary(req)
	require.NoError(t, err)
	require.True(t, runtimeMetaHasMagic(body, runtimeMetaRPCRequestMagicV1[:]))

	got, err := decodeRuntimeMetaRPCRequest(body)
	require.NoError(t, err)
	require.Equal(t, req.Op, got.Op)
	require.Equal(t, req.SlotID, got.SlotID)
	require.Equal(t, byte(1), got.CodecVersion)
}

func TestRuntimeMetaRPCFallbacksToLegacyRequestForUnsupportedPeer(t *testing.T) {
	var versions []byte
	store := New(&proxyTestMigrationCluster{
		nodeID:         2,
		localNodeID:    2,
		slotForKey:     1,
		hashSlotForKey: 0,
		leaders:        map[multiraft.SlotID]multiraft.NodeID{1: 1},
		peers:          map[multiraft.SlotID][]multiraft.NodeID{1: {1}},
		rpcService: func(_ context.Context, _ multiraft.NodeID, _ multiraft.SlotID, _ uint8, payload []byte) ([]byte, error) {
			version, ok := runtimeMetaRPCRequestVersion(payload)
			if !ok {
				return nil, errors.New("invalid test payload")
			}
			versions = append(versions, version)
			if version == 3 {
				return nil, errors.New("nodetransport: remote error: metastore: invalid runtime meta request codec")
			}
			return encodeRuntimeMetaRPCResponseForVersion(runtimeMetaRPCResponse{
				Status: rpcStatusOK,
				Meta:   &metadb.ChannelRuntimeMeta{ChannelID: "g1", ChannelType: 2},
			}, 1)
		},
	}, openTestDB(t))

	resp, err := store.callRuntimeMetaRPC(context.Background(), 1, runtimeMetaRPCRequest{
		Op:          runtimeMetaRPCGet,
		SlotID:      1,
		ChannelID:   "g1",
		ChannelType: 2,
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Meta)
	require.Equal(t, "g1", resp.Meta.ChannelID)
	require.Zero(t, resp.Meta.WriteFenceVersion)
	require.Equal(t, []byte{3, 1}, versions)
}

func TestRuntimeMetaRPCDoesNotFallbackToLegacyOnNonCodecError(t *testing.T) {
	calls := 0
	store := New(&proxyTestMigrationCluster{
		nodeID:         2,
		localNodeID:    2,
		slotForKey:     1,
		hashSlotForKey: 0,
		leaders:        map[multiraft.SlotID]multiraft.NodeID{1: 1},
		peers:          map[multiraft.SlotID][]multiraft.NodeID{1: {1}},
		rpcService: func(_ context.Context, _ multiraft.NodeID, _ multiraft.SlotID, _ uint8, _ []byte) ([]byte, error) {
			calls++
			return nil, errors.New("temporary outage")
		},
	}, openTestDB(t))

	_, err := store.callRuntimeMetaRPC(context.Background(), 1, runtimeMetaRPCRequest{
		Op:          runtimeMetaRPCGet,
		SlotID:      1,
		ChannelID:   "g1",
		ChannelType: 2,
	})
	require.Error(t, err)
	require.Equal(t, 1, calls)
}

func TestRuntimeMetaRPCRejectsJSONPayload(t *testing.T) {
	store := New(nil, openTestDB(t))

	_, err := store.handleRuntimeMetaRPC(context.Background(), []byte(`{"op":"list","slot_id":1}`))
	require.Error(t, err)
}
