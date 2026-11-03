package node

import (
	"bytes"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
	"testing"
)

// Failure inventory: a forwarded proof can be dropped, partly decoded, or
// mislabeled as an older request; none may silently remove the append fence.
func TestPersonDirectoryAppendRPCFence(t *testing.T) {
	cmd := channelAppendTestCommand()
	cmd.PersonDirectoryFence = metadb.PersonDirectoryAppendFence{ChannelEpoch: 2, LeaderEpoch: 3, RouteGeneration: 4, DirectoryGeneration: 5}
	req := channelAppendRequest{Target: channelAppendTestTarget(), Items: []channelAppendItem{{Command: cmd}, {Command: channelAppendTestCommand()}}}
	req.Target.RouteGeneration = 4
	raw, err := encodeChannelAppendRequest(req)
	require.NoError(t, err)
	require.Equal(t, byte(4), raw[4])
	got, err := decodeChannelAppendRequest(raw)
	require.NoError(t, err)
	require.Equal(t, req, got)
	for n := range len(raw) {
		_, err := decodeChannelAppendRequest(raw[:n])
		require.Error(t, err)
	}
	for _, version := range []byte{2, 3} {
		bad := bytes.Clone(raw)
		bad[4] = version
		_, err := decodeChannelAppendRequest(bad)
		require.Error(t, err)
	}
	cmd.PersonDirectoryFence.LeaderEpoch = 0
	req.Items[0].Command = cmd
	_, err = encodeChannelAppendRequest(req)
	require.Error(t, err)
}
