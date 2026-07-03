package cluster

import (
	"context"
	"fmt"
	"testing"
	"time"

	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/plane"
	"github.com/stretchr/testify/require"
)

func TestCompactControllerRaftLogOnNodeRoutesLocalCompaction(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	store := host.raftDB.ForController()
	require.NoError(t, host.service.Propose(context.Background(), controllerNodeJoinCommand(2)))
	require.Eventually(t, func() bool {
		state, err := store.InitialState(context.Background())
		return err == nil && state.AppliedIndex >= 2
	}, 2*time.Second, 10*time.Millisecond)

	result, err := cluster.CompactControllerRaftLogOnNode(context.Background(), uint64(cluster.cfg.NodeID))
	require.NoError(t, err)
	require.Equal(t, uint64(cluster.cfg.NodeID), result.NodeID)
	require.True(t, result.Compacted)
	require.GreaterOrEqual(t, result.AppliedIndex, uint64(2))
	require.Equal(t, result.AppliedIndex, result.AfterSnapshotIndex)
}

func TestControllerHandlerServesControllerRaftCompactionWithoutLeaderRedirect(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	require.NoError(t, host.service.Propose(context.Background(), controllerNodeJoinCommand(2)))
	handler := &controllerHandler{cluster: cluster}
	body, err := encodeControllerRequest(controllerRPCRequest{Kind: controllerRPCControllerRaftCompact})
	require.NoError(t, err)

	respBody, err := handler.Handle(context.Background(), body)
	require.NoError(t, err)
	resp, err := decodeControllerResponse(controllerRPCControllerRaftCompact, respBody)
	require.NoError(t, err)
	require.False(t, resp.NotLeader)
	require.NotNil(t, resp.ControllerRaftCompaction)
	require.Equal(t, uint64(host.localNode), resp.ControllerRaftCompaction.NodeID)
}

func TestControllerRaftCompactionCodecRoundTripsResult(t *testing.T) {
	want := ControllerRaftCompactionResult{
		NodeID:              2,
		AppliedIndex:        42,
		BeforeSnapshotIndex: 30,
		AfterSnapshotIndex:  42,
		Compacted:           true,
	}
	body, err := encodeControllerRequest(controllerRPCRequest{Kind: controllerRPCControllerRaftCompact})
	require.NoError(t, err)
	req, err := decodeControllerRequest(body)
	require.NoError(t, err)
	require.Equal(t, controllerRPCControllerRaftCompact, req.Kind)

	respBody, err := encodeControllerResponse(controllerRPCControllerRaftCompact, controllerRPCResponse{ControllerRaftCompaction: &want})
	require.NoError(t, err)
	resp, err := decodeControllerResponse(controllerRPCControllerRaftCompact, respBody)
	require.NoError(t, err)
	require.Equal(t, want, *resp.ControllerRaftCompaction)
}

func controllerNodeJoinCommand(nodeID uint64) slotcontroller.Command {
	return slotcontroller.Command{
		Kind: slotcontroller.CommandKindNodeJoin,
		NodeJoin: &slotcontroller.NodeJoinRequest{
			NodeID:         nodeID,
			Addr:           fmt.Sprintf("127.0.0.1:%d", 7000+nodeID),
			JoinedAt:       time.Unix(int64(nodeID), 0),
			CapacityWeight: 1,
		},
	}
}
