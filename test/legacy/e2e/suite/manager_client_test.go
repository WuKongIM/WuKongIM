//go:build e2e && legacy_e2e

package suite

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveSlotTopologyRejectsMissingLeader(t *testing.T) {
	body := []byte(`{
		"slot_id":1,
		"state":{"quorum":"ready","sync":"matched"},
		"runtime":{"leader_id":0,"current_peers":[1,2,3],"has_quorum":true}
	}`)

	_, err := parseSlotTopology(1, []uint64{1, 2, 3}, body)
	require.Error(t, err)
}

func TestResolveSlotTopologyRejectsPeerMismatch(t *testing.T) {
	body := []byte(`{
		"slot_id":1,
		"state":{"quorum":"ready","sync":"peer_mismatch"},
		"runtime":{"leader_id":2,"current_peers":[1,2],"has_quorum":true}
	}`)

	_, err := parseSlotTopology(1, []uint64{1, 2, 3}, body)
	require.Error(t, err)
}

func TestResolveSlotTopologyReturnsLeaderAndFollowers(t *testing.T) {
	body := []byte(`{
		"slot_id":1,
		"state":{"quorum":"ready","sync":"matched"},
		"runtime":{"leader_id":2,"current_peers":[1,2,3],"has_quorum":true}
	}`)

	got, err := parseSlotTopology(1, []uint64{1, 2, 3}, body)
	require.NoError(t, err)
	require.Equal(t, uint32(1), got.SlotID)
	require.Equal(t, uint64(2), got.LeaderNodeID)
	require.Equal(t, []uint64{1, 3}, got.FollowerNodeIDs)
	require.Contains(t, got.RawBody, `"leader_id":2`)
}

func TestConnectionsContainUID(t *testing.T) {
	items := []ManagerConnection{
		{UID: "u1"},
		{UID: "u2"},
	}

	require.True(t, connectionsContainUID(items, "u1"))
	require.False(t, connectionsContainUID(items, "u3"))
}

func TestDecodeChannelClusterReplicaDetail(t *testing.T) {
	body := []byte(`{
		"channel": {
			"channel_id": "u1@u2",
			"channel_type": 1,
			"slot_id": 1,
			"hash_slot": 9,
			"channel_epoch": 7,
			"leader_epoch": 3,
			"leader": 1,
			"replicas": [1, 2, 3],
			"isr": [1, 2, 3],
			"min_isr": 2,
			"max_message_seq": 10,
			"status": "active",
			"features": 5,
			"lease_until_ms": 1700000000000
		},
		"runtime_reported": true,
		"commit_seq": 10,
		"min_available_seq": 1,
		"retention_through_seq": 0,
		"replicas": [
			{"node_id": 1, "role": "leader", "is_leader": true, "in_isr": true, "reported": true, "commit_seq": 10, "leo": 10, "checkpoint_hw": 10, "lag": 0},
			{"node_id": 2, "role": "follower", "is_leader": false, "in_isr": true, "reported": false, "commit_seq": null, "leo": null, "checkpoint_hw": null, "lag": null}
		]
	}`)

	got, err := decodeChannelClusterReplicaDetail(body)

	require.NoError(t, err)
	require.Equal(t, "u1@u2", got.Channel.ChannelID)
	require.Equal(t, int64(1), got.Channel.ChannelType)
	require.Equal(t, uint64(1), got.Channel.Leader)
	require.Equal(t, []uint64{1, 2, 3}, got.Channel.Replicas)
	require.Equal(t, []uint64{1, 2, 3}, got.Channel.ISR)
	require.Len(t, got.Replicas, 2)
	require.True(t, got.Replicas[1].InISR)
	require.False(t, got.Replicas[1].IsLeader)
	require.Nil(t, got.Replicas[1].CommitSeq)
}

func TestDecodeChannelLeaderTransferResponse(t *testing.T) {
	body := []byte(`{
		"changed": true,
		"channel": {
			"channel_id": "u1@u2",
			"channel_type": 1,
			"slot_id": 1,
			"hash_slot": 9,
			"channel_epoch": 7,
			"leader_epoch": 4,
			"leader": 2,
			"replicas": [1, 2, 3],
			"isr": [1, 2, 3],
			"min_isr": 2,
			"max_message_seq": 10,
			"status": "active",
			"features": 5,
			"lease_until_ms": 1700000001000
		}
	}`)

	got, err := decodeChannelLeaderTransferResponse(body)

	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Equal(t, uint64(2), got.Channel.Leader)
	require.Equal(t, uint64(4), got.Channel.LeaderEpoch)
}

func TestDecodeChannelMigrationDetailResponse(t *testing.T) {
	body := []byte(`{
		"task_id": "task-1",
		"kind": "replica_migration",
		"status": "running",
		"phase": "DrainLeader",
		"channel_id": "u1@u2",
		"channel_type": 1,
		"source_node": 3,
		"target_node": 2,
		"desired_leader": 2,
		"base_channel_epoch": 7,
		"base_leader_epoch": 4,
		"current_channel_epoch": 8,
		"current_leader_epoch": 5,
		"leader_leo": 12,
		"leader_hw": 11,
		"target_leo": 10,
		"target_checkpoint_hw": 9,
		"lag_records": 2,
		"fence_active": true,
		"fence_until_ms": 1700000000000,
		"blocker_code": "leader_drain_pending",
		"blocker_message": "waiting for leader drain proof",
		"attempt": 3,
		"next_run_at_ms": 1700000000100,
		"last_error": "leader still accepts writes"
	}`)

	got, err := decodeChannelMigrationDetailResponse(body)

	require.NoError(t, err)
	require.Equal(t, "task-1", got.TaskID)
	require.Equal(t, "DrainLeader", got.Phase)
	require.Equal(t, uint64(3), got.SourceNode)
	require.Equal(t, uint64(2), got.TargetNode)
	require.Equal(t, uint64(2), got.LagRecords)
	require.True(t, got.FenceActive)
	require.Equal(t, "leader_drain_pending", got.BlockerCode)
	require.Equal(t, "leader still accepts writes", got.LastError)
}

func TestPersonChannelID(t *testing.T) {
	left := PersonChannelID("u1", "u2")
	right := PersonChannelID("u2", "u1")

	require.NotEmpty(t, left)
	require.Equal(t, left, right)
	require.Contains(t, left, "@")
}

func TestDecodeManagerNodesResponse(t *testing.T) {
	body := []byte(`{
		"total": 2,
		"items": [{
			"node_id": 1,
			"addr": "127.0.0.1:17001",
			"status": "alive",
			"is_local": true,
			"controller": {"role": "leader"},
			"slot_stats": {"count": 1, "leader_count": 1}
		}, {
			"node_id": 4,
			"addr": "127.0.0.1:17004",
			"status": "alive",
			"is_local": false,
			"controller": {"role": "none"},
			"slot_stats": {"count": 0, "leader_count": 0}
		}]
	}`)

	resp, err := decodeManagerNodesResponse(body)

	require.NoError(t, err)
	require.Equal(t, 2, resp.Total)
	require.Len(t, resp.Items, 2)
	require.Equal(t, uint64(4), resp.Items[1].NodeID)
	require.Equal(t, "127.0.0.1:17004", resp.Items[1].Addr)
	require.Equal(t, "alive", resp.Items[1].Status)
	require.Equal(t, "none", resp.Items[1].Controller.Role)
}

func TestManagerNodesFindsNodeByID(t *testing.T) {
	items := []ManagerNode{
		{NodeID: 1},
		{NodeID: 4, Addr: "127.0.0.1:17004"},
	}

	node, ok := managerNodeByID(items, 4)

	require.True(t, ok)
	require.Equal(t, "127.0.0.1:17004", node.Addr)
	_, ok = managerNodeByID(items, 9)
	require.False(t, ok)
}

func TestDecodeNodeOnboardingResponses(t *testing.T) {
	candidatesBody := []byte(`{
		"total": 1,
		"items": [{
			"node_id": 4,
			"addr": "127.0.0.1:17004",
			"status": "alive",
			"join_state": "active",
			"slot_count": 0,
			"leader_count": 0,
			"recommended": true
		}]
	}`)
	candidates, err := decodeNodeOnboardingCandidatesResponse(candidatesBody)
	require.NoError(t, err)
	require.Len(t, candidates.Items, 1)
	require.Equal(t, uint64(4), candidates.Items[0].NodeID)
	require.True(t, candidates.Items[0].Recommended)

	jobBody := []byte(`{
		"job_id": "onboard-1",
		"target_node_id": 4,
		"status": "completed",
		"plan": {
			"target_node_id": 4,
			"summary": {
				"current_target_slot_count": 0,
				"planned_target_slot_count": 1,
				"current_target_leader_count": 0,
				"planned_leader_gain": 1
			},
			"moves": [{
				"slot_id": 1,
				"source_node_id": 1,
				"target_node_id": 4,
				"desired_peers_before": [1,2,3],
				"desired_peers_after": [2,3,4],
				"leader_transfer_required": true
			}],
			"blocked_reasons": []
		},
		"moves": [{"slot_id": 1, "status": "completed"}],
		"result_counts": {"pending":0,"running":0,"completed":1,"failed":0,"skipped":0}
	}`)
	job, err := decodeNodeOnboardingJobResponse(jobBody)
	require.NoError(t, err)
	require.Equal(t, "onboard-1", job.JobID)
	require.Equal(t, "completed", job.Status)
	require.Equal(t, 1, job.ResultCounts.Completed)
	require.Len(t, job.Plan.Moves, 1)
	require.Equal(t, []uint64{2, 3, 4}, job.Plan.Moves[0].DesiredPeersAfter)
}

func TestDecodeSlotRaftCompactionResponse(t *testing.T) {
	body := []byte(`{
		"total": 1,
		"succeeded": 1,
		"failed": 0,
		"items": [{
			"node_id": 2,
			"slot_id": 1,
			"success": true,
			"applied_index": 12,
			"before_snapshot_index": 3,
			"after_snapshot_index": 12,
			"compacted": true
		}]
	}`)

	resp, err := decodeSlotRaftCompactionResponse(body)

	require.NoError(t, err)
	require.Equal(t, 1, resp.Total)
	require.Equal(t, 1, resp.Succeeded)
	require.Len(t, resp.Items, 1)
	require.Equal(t, uint64(2), resp.Items[0].NodeID)
	require.Equal(t, uint32(1), resp.Items[0].SlotID)
	require.True(t, resp.Items[0].Compacted)
	require.Equal(t, uint64(12), resp.Items[0].AfterSnapshotIndex)
}

func TestDecodeControllerRaftResponses(t *testing.T) {
	statusBody := []byte(`{
		"node_id": 2,
		"role": "follower",
		"leader_id": 1,
		"health": "healthy",
		"applied_index": 42,
		"snapshot_index": 40,
		"restore": {"last_snapshot_index": 40, "last_snapshot_term": 3},
		"peers": []
	}`)
	status, err := decodeControllerRaftStatusResponse(statusBody)
	require.NoError(t, err)
	require.Equal(t, uint64(2), status.NodeID)
	require.Equal(t, "follower", status.Role)
	require.Equal(t, uint64(40), status.Restore.LastSnapshotIndex)

	compactBody := []byte(`{
		"total": 1,
		"succeeded": 1,
		"failed": 0,
		"items": [{
			"node_id": 1,
			"success": true,
			"applied_index": 45,
			"before_snapshot_index": 12,
			"after_snapshot_index": 45,
			"compacted": true
		}]
	}`)
	compacted, err := decodeControllerRaftCompactionResponse(compactBody)
	require.NoError(t, err)
	require.Equal(t, 1, compacted.Total)
	require.Len(t, compacted.Items, 1)
	require.Equal(t, uint64(45), compacted.Items[0].AfterSnapshotIndex)
}
