//go:build e2e

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
