package channels

import (
	"bytes"
	"fmt"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func benchmarkConversationResponse(count int) ConversationHeadsResponse {
	response := ConversationHeadsResponse{Items: make([]ConversationHeadResult, count)}
	for i := range response.Items {
		response.Items[i].Head = ConversationHead{Found: true, ReadThroughSeq: 300, Message: ch.Message{
			MessageID: 1000000 + uint64(i), MessageSeq: 300, ChannelID: fmt.Sprintf("conversation-qps-channel-%d", i), ChannelType: 2,
			FromUID: "sender", ClientMsgNo: "client-message", ServerTimestampMS: 1789459200000, Payload: bytes.Repeat([]byte{'x'}, 128),
		}}
	}
	return response
}

// The real batch encoder must not repeatedly grow and copy each payload-bearing
// response. The fixed budget leaves one returned frame and interface boxing.
func TestConversationResponseAllocationBudget(t *testing.T) {
	response := benchmarkConversationResponse(100)
	allocations := testing.AllocsPerRun(20, func() {
		if _, err := encodeConversationHeadsResponse(response); err != nil {
			t.Fatal(err)
		}
	})
	if allocations > 2 {
		t.Fatalf("batch response allocations = %.0f, want <= 2", allocations)
	}
}

func BenchmarkConversationResponseEncoding(b *testing.B) {
	for _, count := range []int{25, 100, 200} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			response := benchmarkConversationResponse(count)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := encodeConversationHeadsResponse(response); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Compare the complete frame with the previous two-buffer composition, for
// every supported version and payload presence shape. Sizing must not alter
// either the wire format or ownership of the returned bytes.
func TestReadResponseFrameCompatibility(t *testing.T) {
	for _, version := range []uint8{5, 6, 7, 8, 9, 10} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			heads := benchmarkConversationResponse(3)
			heads.Items[0].Head.Message.Payload = nil
			heads.Items[0].Head.Message.ServerTimestampMS = -1 << 63
			heads.Items[1].Head.Message.Payload = []byte{}
			heads.Items[1].Head.Message.MessageID = ^uint64(0)
			heads.Items[1].Head.NonBusinessUnread = 128
			heads.Items[1].Head.UnreadBoundary = ^uint64(0)
			heads.Items[1].Head.BoundaryComputed = true
			heads.Items[2].Head.Message.TraceID = "trace"
			if version >= 8 {
				heads.Items[2].Head.Message.RedDot = true
				heads.Items[2].Head.Message.SyncOnce = true
			}
			if version >= 9 {
				heads.Items[2].Head.Message.Expire = ^uint32(0)
			}
			reads := CommittedReadsResponse{Items: []CommittedReadResult{{}, {}, {}}}
			reads.Items[1].Read.Messages = []ch.Message{}
			reads.Items[2].Read.Messages = []ch.Message{heads.Items[0].Head.Message, heads.Items[1].Head.Message, heads.Items[2].Head.Message}
			reads.Items[2].Read.NextSeq = ^uint64(0)
			cases := []struct {
				kind    uint8
				payload any
			}{
				{kindConversationHeadsResponse, heads},
				{kindConversationHeadsResponse, ConversationHeadsResponse{}},
				{kindConversationHeadsResponse, ConversationHeadsResponse{Items: []ConversationHeadResult{{}}}},
				{kindCommittedReadsResponse, reads},
				{kindCommittedReadsResponse, CommittedReadsResponse{}},
			}
			for _, tc := range cases {
				body, ok := appendRPCPayload([]byte{rpcResultOK}, tc.payload, version)
				if !ok {
					t.Fatal("unsupported fixture")
				}
				want := encodeFrameVersion(version, tc.kind, body)
				got, err := encodeRPCResultVersion(version, tc.kind, tc.payload, nil)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Fatal("wire bytes changed")
				}
				if size := readResponseFrameSize(tc.payload, version); size != len(want) {
					t.Fatalf("size = %d, want %d", size, len(want))
				}
			}
			// Mutating a returned frame must never mutate a source message payload.
			frame, err := encodeRPCResultVersion(version, kindConversationHeadsResponse, heads, nil)
			if err != nil {
				t.Fatal(err)
			}
			for i := range frame {
				frame[i] = 0
			}
			if !bytes.Equal(heads.Items[2].Head.Message.Payload, bytes.Repeat([]byte{'x'}, 128)) {
				t.Fatal("encoder borrowed source payload")
			}
		})
	}
}
