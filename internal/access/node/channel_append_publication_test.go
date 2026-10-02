package node

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

func TestPublicationAppendRPCVersionThreeAndOwnedContent(t *testing.T) {
	metadata, err := hex.DecodeString("01010100000000000003e800016e00016300017400030600017800036f6e65020000003c06000178000374776f")
	require.NoError(t, err)
	cmd := channelAppendTestCommand()
	cmd.PublicationMetadata = bytes.Clone(metadata)
	req := channelAppendRequest{Target: channelAppendTestTarget(), Items: []channelAppendItem{{Command: cmd}, {Command: channelAppendTestCommand()}}}
	raw, err := encodeChannelAppendRequest(req)
	require.NoError(t, err)
	require.Equal(t, byte(3), raw[4])
	decoded, err := decodeChannelAppendRequest(raw)
	require.NoError(t, err)
	require.Equal(t, req, decoded)
	for n := 0; n < len(raw); n++ {
		_, err := decodeChannelAppendRequest(raw[:n])
		require.Error(t, err)
	}
	corrupted := bytes.Clone(raw)
	at := bytes.Index(corrupted, metadata)
	require.GreaterOrEqual(t, at, 0)
	corrupted[at] = 2
	_, err = decodeChannelAppendRequest(corrupted)
	require.Error(t, err)
	mislabeled := bytes.Clone(raw)
	mislabeled[4] = 2
	_, err = decodeChannelAppendRequest(mislabeled)
	require.Error(t, err)
	clear(raw)
	clear(cmd.PublicationMetadata)
	require.Equal(t, metadata, decoded.Items[0].Command.PublicationMetadata)
	for _, bad := range [][]byte{{1}, {2}, make([]byte, publication.MaxEncodedBytes+1)} {
		req.Items[0].Command.PublicationMetadata = bad
		_, err := encodeChannelAppendRequest(req)
		require.Error(t, err)
	}
}

func TestPublicationAppendRPCNativeV2Fixture(t *testing.T) {
	req := channelAppendRequest{Target: channelAppendTestTarget(), Items: []channelAppendItem{{Command: channelAppendTestCommand()}}}
	raw, err := encodeChannelAppendRequest(req)
	require.NoError(t, err)
	require.Equal(t, byte(2), raw[4])
	require.Equal(t, "574b564102026731020b6368616e6e656c2d6b657903040501010701027531026431030b0c0d08636c69656e742d310774726163652d310b6368616e6e656c2d6b6579026731020707746f7069632d31901c0568656c6c6f010101010102027532027533630406706c7567696e020100", hex.EncodeToString(raw))
	decoded, err := decodeChannelAppendRequest(raw)
	require.NoError(t, err)
	require.Equal(t, req, decoded)
}
