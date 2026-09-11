package channels

import (
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestExpireCodecPreservesFieldsAndRefusesLossyFallback(t *testing.T) {
	for _, expire := range []uint32{0, 3600, ^uint32(0)} {
		msg := ch.Message{MessageID: 11, MessageSeq: 1, ChannelID: "room", ChannelType: 2, Expire: expire, RedDot: true, SyncOnce: true, Payload: []byte("body")}
		req := ch.AppendBatchRequest{ChannelID: ch.ChannelID{ID: "room", Type: 2}, Messages: []ch.Message{msg}}
		raw, err := encodeAppendBatchRequest(req)
		require.NoError(t, err)
		got, err := decodeAppendBatchRequest(raw)
		require.NoError(t, err)
		require.Equal(t, req, got)
		for _, p := range []struct {
			kind          uint8
			value, target any
		}{
			{kindAppendResponse, ch.AppendResult{Message: msg}, &ch.AppendResult{}},
			{kindAppendBatchResponse, ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{Message: msg}}}, &ch.AppendBatchResult{}},
			{kindPullResponse, channeltransport.PullResponse{Records: []ch.Record{{ID: 11, Index: 1, Expire: expire, RedDot: true, SyncOnce: true}}}, &channeltransport.PullResponse{}},
			{kindLastVisibleResponse, LastVisibleResponse{Message: msg, Found: true}, &LastVisibleResponse{}},
			{kindConversationHeadsResponse, ConversationHeadsResponse{Items: []ConversationHeadResult{{Head: ConversationHead{Message: msg, Found: true}}}}, &ConversationHeadsResponse{}},
		} {
			raw, err := encodeRPCResult(p.kind, p.value, nil)
			require.NoError(t, err)
			require.NoError(t, decodeRPCResult(raw, p.kind, p.target))
			require.Equal(t, p.value, dereferenceRedDotPayload(p.target))
			old, err := encodeRPCResultVersion(legacyCodecVersionV8, p.kind, p.value, nil)
			require.NoError(t, err)
			err = decodeRPCResult(old, p.kind, p.target)
			if expire != 0 {
				require.ErrorContains(t, err, "expire requires")
			} else {
				require.NoError(t, err)
				require.Equal(t, p.value, dereferenceRedDotPayload(p.target))
			}
		}
		old, err := encodeAppendBatchRequestVersion(req, legacyCodecVersionV8)
		if expire != 0 {
			require.ErrorIs(t, err, errExpireCodecRequired)
		} else {
			require.NoError(t, err)
			got, err := decodeAppendBatchRequest(old)
			require.NoError(t, err)
			require.Equal(t, req, got)
			require.Equal(t, legacyCodecVersionV8, responseCodecVersion(old))
		}
	}
	client := &TransportClient{}
	calls := 0
	_, err := client.callCompatible(2, false, func(version uint8) ([]byte, error) {
		return encodeAppendRequestVersion(ch.AppendRequest{Message: ch.Message{Expire: 3600}}, version)
	}, func(payload []byte) ([]byte, error) { calls++; return nil, errInvalidCodecFrame })
	require.ErrorIs(t, err, errExpireCodecRequired)
	require.Equal(t, 1, calls, "no legacy request may discard the lifetime")
}

func TestExpireCodecRejectsOverflowAndTruncation(t *testing.T) {
	_, _, err := readExpiry(appendUvarint(nil, 1<<32), 0)
	require.Error(t, err)
	_, _, err = readExpiry([]byte{0x80}, 0)
	require.Error(t, err)
}
