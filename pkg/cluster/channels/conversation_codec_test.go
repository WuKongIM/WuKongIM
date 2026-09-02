package channels

import (
	"errors"
	"reflect"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

func TestConversationHeadsCodecPreservesAlignedRoutingFences(t *testing.T) {
	request := ConversationHeadsRequest{
		UID: "user-一",
		Items: []ConversationHeadRequest{
			{ChannelID: ch.ChannelID{ID: "room-a", Type: 1}, RetentionThroughSeq: 9, ExpectedLeader: 2, ExpectedChannelEpoch: 3, ExpectedLeaderEpoch: 4, ExpectedMinISR: 2},
			{ChannelID: ch.ChannelID{ID: "room-b", Type: 2}, RetentionThroughSeq: 19, ExpectedLeader: 5, ExpectedChannelEpoch: 6, ExpectedLeaderEpoch: 7, ExpectedMinISR: 3},
		},
	}
	for _, version := range []uint8{legacyCodecVersionV5, legacyCodecVersionV6, codecVersion} {
		encoded, err := encodeConversationHeadsRequestVersion(request, version)
		if err != nil {
			t.Fatalf("encode request version %d: %v", version, err)
		}
		decoded, err := decodeConversationHeadsRequest(encoded)
		if err != nil {
			t.Fatalf("decode request version %d: %v", version, err)
		}
		if !reflect.DeepEqual(decoded, request) {
			t.Fatalf("request version %d round trip = %+v, want %+v", version, decoded, request)
		}
		assertEveryStrictPrefixRejected(t, encoded, func(data []byte) error {
			_, err := decodeConversationHeadsRequest(data)
			return err
		})
		if _, err := decodeConversationHeadsRequest(append(encoded, 0)); err == nil {
			t.Fatalf("request version %d accepted trailing bytes", version)
		}
	}

	for _, items := range [][]ConversationHeadRequest{nil, {}} {
		request := ConversationHeadsRequest{Items: items}
		encoded, err := encodeConversationHeadsRequest(request)
		if err != nil {
			t.Fatalf("encode nil/empty request: %v", err)
		}
		decoded, err := decodeConversationHeadsRequest(encoded)
		if err != nil || !reflect.DeepEqual(decoded.Items, items) {
			t.Fatalf("nil/empty request round trip = %#v, %v; want %#v", decoded.Items, err, items)
		}
	}

	response := ConversationHeadsResponse{Items: []ConversationHeadResult{
		{Head: ConversationHead{Found: true, LastCommittedSeq: 12, RetentionThroughSeq: 4, CurrentUserLastSendSeq: 11, Message: codecContractMessage(12)}},
		{Err: ch.ErrNotLeader},
	}}
	encoded, err := encodeConversationHeadsResponse(response)
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	decoded, err := decodeConversationHeadsResponse(encoded)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(decoded.Items) != 2 || !reflect.DeepEqual(decoded.Items[0].Head, response.Items[0].Head) || !errors.Is(decoded.Items[1].Err, ch.ErrNotLeader) {
		t.Fatalf("response round trip = %+v", decoded)
	}
	assertEveryStrictPrefixRejected(t, encoded, func(data []byte) error {
		_, err := decodeConversationHeadsResponse(data)
		return err
	})
}

func TestCommittedReadsCodecPreservesBoundsDirectionAndItemErrors(t *testing.T) {
	request := CommittedReadsRequest{Items: []CommittedReadRequest{
		{
			CommittedRead: CommittedRead{
				ChannelID: ch.ChannelID{ID: "room-a", Type: 1},
				Request:   channelstore.ReadCommittedRequest{FromSeq: 20, MaxSeq: 50, MinSeq: 5, Limit: 100, MaxBytes: 4096, Reverse: true},
			},
			RetentionThroughSeq: 4, ExpectedLeader: 2, ExpectedChannelEpoch: 3, ExpectedLeaderEpoch: 4, ExpectedMinISR: 2,
		},
		{
			CommittedRead: CommittedRead{
				ChannelID: ch.ChannelID{ID: "room-b", Type: 2},
				Request:   channelstore.ReadCommittedRequest{FromSeq: 1, MaxSeq: 10, MinSeq: 1, Limit: -1, MaxBytes: -2},
			},
			ExpectedLeader: 5, ExpectedChannelEpoch: 6, ExpectedLeaderEpoch: 7, ExpectedMinISR: 1,
		},
	}}
	for _, version := range []uint8{legacyCodecVersionV5, legacyCodecVersionV6, codecVersion} {
		encoded, err := encodeCommittedReadsRequestVersion(request, version)
		if err != nil {
			t.Fatalf("encode request version %d: %v", version, err)
		}
		decoded, err := decodeCommittedReadsRequest(encoded)
		if err != nil || !reflect.DeepEqual(decoded, request) {
			t.Fatalf("request version %d round trip = %+v, %v; want %+v", version, decoded, err, request)
		}
		assertEveryStrictPrefixRejected(t, encoded, func(data []byte) error {
			_, err := decodeCommittedReadsRequest(data)
			return err
		})
		if _, err := decodeCommittedReadsRequest(append(encoded, 0)); err == nil {
			t.Fatalf("request version %d accepted trailing bytes", version)
		}
	}

	response := CommittedReadsResponse{Items: []CommittedReadResult{
		{Read: channelstore.ReadCommittedResult{Messages: []ch.Message{codecContractMessage(8), codecContractMessage(9)}, NextSeq: 10}},
		{Err: ch.ErrStaleMeta},
	}}
	encoded, err := encodeRPCResult(kindCommittedReadsResponse, response, nil)
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	decoded, err := decodeCommittedReadsResponse(encoded)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(decoded.Items) != 2 || !reflect.DeepEqual(decoded.Items[0].Read, response.Items[0].Read) || !errors.Is(decoded.Items[1].Err, ch.ErrStaleMeta) {
		t.Fatalf("response round trip = %+v", decoded)
	}
	assertEveryStrictPrefixRejected(t, encoded, func(data []byte) error {
		_, err := decodeCommittedReadsResponse(data)
		return err
	})
}

func TestConversationCodecRejectsInvalidFrameVersionKindAndOverflow(t *testing.T) {
	request := ConversationHeadsRequest{Items: []ConversationHeadRequest{{ChannelID: ch.ChannelID{ID: "room", Type: 1}}}}
	if _, err := encodeConversationHeadsRequestVersion(request, legacyCodecVersionV4); !errors.Is(err, errInvalidCodecFrame) {
		t.Fatalf("legacy v4 encode error = %v, want errInvalidCodecFrame", err)
	}
	encoded, err := encodeConversationHeadsRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	badVersion := append([]byte(nil), encoded...)
	badVersion[0] = 99
	if _, err := decodeConversationHeadsRequest(badVersion); !errors.Is(err, errInvalidCodecFrame) {
		t.Fatalf("unknown version error = %v, want errInvalidCodecFrame", err)
	}
	badKind := append([]byte(nil), encoded...)
	badKind[1] = kindCommittedReads
	if _, err := decodeConversationHeadsRequest(badKind); !errors.Is(err, errInvalidCodecFrame) {
		t.Fatalf("wrong kind error = %v, want errInvalidCodecFrame", err)
	}

	// Build the body directly so the decoder must reject an integer that cannot
	// be represented by the local int used for quorum admission.
	body := appendString(nil, "user")
	body = appendSliceHeader(body, 1, false)
	body = appendChannelID(body, ch.ChannelID{ID: "room", Type: 1})
	body = appendUvarint(body, 0)
	body = appendUvarint(body, 1)
	body = appendUvarint(body, 1)
	body = appendUvarint(body, 1)
	body = appendUvarint(body, ^uint64(0))
	overflowFrame := encodeFrameVersion(codecVersion, kindConversationHeads, body)
	if _, err := decodeConversationHeadsRequest(overflowFrame); err == nil {
		t.Fatal("conversation head request accepted an overflowing min ISR")
	}
}

func assertEveryStrictPrefixRejected(t *testing.T, encoded []byte, decode func([]byte) error) {
	t.Helper()
	for size := 0; size < len(encoded); size++ {
		if err := decode(encoded[:size]); err == nil {
			t.Fatalf("decoder accepted truncated frame of %d/%d bytes", size, len(encoded))
		}
	}
}

func codecContractMessage(seq uint64) ch.Message {
	return ch.Message{
		MessageSeq: seq, MessageID: 1000 + seq, ChannelID: "room-a", ChannelType: 1,
		ClientMsgNo: "client", FromUID: "user",
		Payload: []byte{byte(seq), 2, 3}, ServerTimestampMS: 1234,
	}
}
