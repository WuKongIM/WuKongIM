//go:build e2e && legacy_e2e

package suite

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeSlotSnapshotUsersResponse(t *testing.T) {
	body := []byte(`{
		"dataset": "cluster/slot-snapshot-users",
		"prefix": "snap-user",
		"count": 2,
		"payload_bytes": 16384,
		"first_uid": "snap-user-000000",
		"last_uid": "snap-user-000001"
	}`)

	resp, err := decodeSlotSnapshotUsersResponse(body)

	require.NoError(t, err)
	require.Equal(t, "cluster/slot-snapshot-users", resp.Dataset)
	require.Equal(t, "snap-user", resp.Prefix)
	require.Equal(t, 2, resp.Count)
	require.Equal(t, 16384, resp.PayloadBytes)
	require.Equal(t, "snap-user-000000", resp.FirstUID)
	require.Equal(t, "snap-user-000001", resp.LastUID)
}

func TestDecodeControllerSnapshotJobsResponse(t *testing.T) {
	body := []byte(`{
		"dataset": "cluster/controller-snapshot-jobs",
		"prefix": "snap-job",
		"target_node_id": 2,
		"count": 3,
		"payload_bytes": 65536,
		"first_job_id": "job-000001",
		"last_job_id": "job-000003"
	}`)

	resp, err := decodeControllerSnapshotJobsResponse(body)

	require.NoError(t, err)
	require.Equal(t, "cluster/controller-snapshot-jobs", resp.Dataset)
	require.Equal(t, "snap-job", resp.Prefix)
	require.Equal(t, uint64(2), resp.TargetNodeID)
	require.Equal(t, 3, resp.Count)
	require.Equal(t, 65536, resp.PayloadBytes)
	require.Equal(t, "job-000001", resp.FirstJobID)
	require.Equal(t, "job-000003", resp.LastJobID)
}
