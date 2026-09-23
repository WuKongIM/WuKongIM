package channels

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

func publicationCodecMessage(t *testing.T) ch.Message {
	t.Helper()
	metadata, err := hex.DecodeString("01010100000000000003e800016e00016300017400030600017800036f6e65020000003c06000178000374776f")
	require.NoError(t, err)
	msg := codecContractMessage(1)
	msg.PublicationMetadata = metadata
	return msg
}

func TestPublicationChannelRPCPreservesContentAndRefusesLossyFallback(t *testing.T) {
	msg := publicationCodecMessage(t)
	record := ch.Record{ID: msg.MessageID, Index: 1, Epoch: 1, FromUID: msg.FromUID, ClientMsgNo: msg.ClientMsgNo, Payload: msg.Payload, ServerTimestampMS: msg.ServerTimestampMS, PublicationMetadata: msg.PublicationMetadata, SizeBytes: len(msg.Payload) + len(msg.PublicationMetadata)}
	req := ch.AppendRequest{ChannelID: ch.ChannelID{ID: "room", Type: 2}, Message: msg}
	raw, err := encodeAppendRequest(req)
	require.NoError(t, err)
	require.Equal(t, byte(11), raw[0])
	decoded, err := decodeAppendRequest(raw)
	require.NoError(t, err)
	require.Equal(t, req, decoded)
	assertEveryStrictPrefixRejected(t, raw, func(b []byte) error { _, err := decodeAppendRequest(b); return err })
	clear(raw)
	require.Equal(t, msg.PublicationMetadata, decoded.Message.PublicationMetadata)

	batch := ch.AppendBatchRequest{ChannelID: req.ChannelID, Messages: []ch.Message{msg, codecContractMessage(2)}}
	raw, err = encodeAppendBatchRequest(batch)
	require.NoError(t, err)
	gotBatch, err := decodeAppendBatchRequest(raw)
	require.NoError(t, err)
	require.Equal(t, batch, gotBatch)

	for _, tc := range []struct {
		kind          uint8
		value, target any
	}{
		{kindAppendResponse, ch.AppendResult{Message: msg}, &ch.AppendResult{}},
		{kindAppendBatchResponse, ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{Message: msg}}}, &ch.AppendBatchResult{}},
		{kindPullResponse, channeltransport.PullResponse{Records: []ch.Record{record}}, &channeltransport.PullResponse{}},
		{kindPullBatchResponse, channeltransport.PullBatchResponse{Items: []channeltransport.PullBatchItemResult{{Response: channeltransport.PullResponse{Records: []ch.Record{record}}}}}, &channeltransport.PullBatchResponse{}},
		{kindLastVisibleResponse, LastVisibleResponse{Message: msg, Found: true}, &LastVisibleResponse{}},
		{kindConversationHeadsResponse, ConversationHeadsResponse{Items: []ConversationHeadResult{{Head: ConversationHead{Message: msg, Found: true, NonBusinessUnread: 3, UnreadBoundary: 5, BoundaryComputed: true}}}}, &ConversationHeadsResponse{}},
		{kindCommittedReadsResponse, CommittedReadsResponse{Items: []CommittedReadResult{{Read: channelstore.ReadCommittedResult{Messages: []ch.Message{msg}, NextSeq: 2}}}}, &CommittedReadsResponse{}},
	} {
		raw, err := encodeRPCResult(tc.kind, tc.value, nil)
		require.NoError(t, err)
		require.NoError(t, decodeRPCResult(raw, tc.kind, tc.target))
		require.Equal(t, tc.value, reflect.ValueOf(tc.target).Elem().Interface())
		if tc.kind == kindConversationHeadsResponse || tc.kind == kindCommittedReadsResponse {
			require.Equal(t, len(raw), readResponseFrameSize(tc.value, 11), "frame reservation omitted metadata")
		}
		assertEveryStrictPrefixRejected(t, raw, func(b []byte) error { return decodeRPCResult(b, tc.kind, tc.target) })
		require.NoError(t, decodeRPCResult(raw, tc.kind, tc.target))
		clear(raw)
		require.Equal(t, tc.value, reflect.ValueOf(tc.target).Elem().Interface(), "decoder borrowed metadata")
		for _, version := range []uint8{5, 8, 9, 10} {
			old, err := encodeRPCResultVersion(version, tc.kind, tc.value, nil)
			require.NoError(t, err)
			require.ErrorContains(t, decodeRPCResult(old, tc.kind, tc.target), "publication metadata requires")
		}
	}
	for _, version := range []uint8{5, 8, 9, 10} {
		_, err := encodeAppendRequestVersion(req, version)
		require.ErrorContains(t, err, "publication metadata requires")
		_, err = encodeAppendBatchRequestVersion(batch, version)
		require.ErrorContains(t, err, "publication metadata requires")
	}
	client := &TransportClient{}
	calls := 0
	_, err = client.callCompatible(2, false, func(version uint8) ([]byte, error) { return encodeAppendRequestVersion(req, version) }, func([]byte) ([]byte, error) { calls++; return nil, errInvalidCodecFrame })
	require.ErrorContains(t, err, "publication metadata requires")
	require.Equal(t, 1, calls, "never send a lossy fallback")
}

func TestPublicationChannelRPCRejectsInvalidValues(t *testing.T) {
	for _, metadata := range [][]byte{{1}, {2}, bytes.Repeat([]byte{1}, publication.MaxEncodedBytes+1)} {
		msg := publicationCodecMessage(t)
		msg.PublicationMetadata = metadata
		_, err := encodeAppendRequest(ch.AppendRequest{Message: msg})
		require.Error(t, err)
		_, err = encodeAppendBatchRequest(ch.AppendBatchRequest{Messages: []ch.Message{msg}})
		require.Error(t, err)
		// Build raw bytes directly to ensure decode validation is independent of encoders.
		raw := encodeFrameVersion(11, kindAppend, appendAppendRequest(nil, ch.AppendRequest{Message: msg}, 11))
		_, err = decodeAppendRequest(raw)
		require.Error(t, err)
		raw = append([]byte{11, kindAppendResponse, rpcResultOK}, appendAppendResult(nil, ch.AppendResult{Message: msg}, 11)...)
		require.Error(t, decodeRPCResult(raw, kindAppendResponse, &ch.AppendResult{}))
		encoded, err := encodeRPCResult(kindAppendResponse, ch.AppendResult{Message: msg}, nil)
		require.NoError(t, err)
		require.Error(t, decodeRPCResult(encoded, kindAppendResponse, &ch.AppendResult{}))
	}
}

func TestPublicationNativeV10WireFixture(t *testing.T) {
	msg := codecContractMessage(1)
	raw, err := encodeAppendRequestVersion(ch.AppendRequest{Message: msg}, 10)
	require.NoError(t, err)
	require.Equal(t, "0a060000e9070106726f6f6d2d6101047573657206636c69656e74a4130000000103010203000000000000", hex.EncodeToString(raw))
	got, err := decodeAppendRequest(raw)
	require.NoError(t, err)
	require.Equal(t, msg, got.Message)
	require.Equal(t, uint8(10), responseCodecVersion(raw))
	resp := ConversationHeadsResponse{Items: []ConversationHeadResult{{Head: ConversationHead{Found: true, Message: msg, NonBusinessUnread: 7, UnreadBoundary: 9, BoundaryComputed: true}}}}
	raw, err = encodeRPCResultVersion(10, kindConversationHeadsResponse, resp, nil)
	require.NoError(t, err)
	require.Equal(t, "0a110001010001e9070106726f6f6d2d6101047573657206636c69656e74a4130000000103010203000000000000070901", hex.EncodeToString(raw))
	var decoded ConversationHeadsResponse
	require.NoError(t, decodeRPCResult(raw, kindConversationHeadsResponse, &decoded))
	require.Equal(t, resp, decoded)
	require.Equal(t, len(raw), readResponseFrameSize(resp, 10))
}
