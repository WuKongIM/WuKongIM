package cluster

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

func publicationFixture(t *testing.T) []byte {
	t.Helper()
	b, err := hex.DecodeString("01010100000000000003e800016e00016300017400030600017800036f6e65020000003c06000178000374776f")
	require.NoError(t, err)
	return b
}

func TestPublicationAppenderPreservesIndependentContent(t *testing.T) {
	metadata := publicationFixture(t)
	input := bytes.Clone(metadata)
	node := &recordingNode{result: ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{Message: ch.Message{PublicationMetadata: bytes.Clone(metadata)}}}}}
	result, err := NewChannelAppender(node).AppendBatch(context.Background(), channelappend.AppendBatchRequest{Messages: []channelappend.Message{{PublicationMetadata: input}}})
	require.NoError(t, err)
	require.Len(t, node.last.Messages, 1)
	require.Equal(t, metadata, node.last.Messages[0].PublicationMetadata)
	clear(input)
	require.Equal(t, metadata, node.last.Messages[0].PublicationMetadata)
	require.Equal(t, metadata, result.Items[0].Message.PublicationMetadata)
	clear(result.Items[0].Message.PublicationMetadata)
	require.Equal(t, metadata, node.result.Items[0].Message.PublicationMetadata)
}

func TestPublicationIdempotencyMatchesContentAndKeepsOriginalClock(t *testing.T) {
	original := publicationFixture(t)
	for _, change := range []string{"clock", "qos", "publisher", "property", "body", "native", "malformed"} {
		t.Run(change, func(t *testing.T) {
			wanted := bytes.Clone(original)
			body := []byte("body")
			switch change {
			case "clock":
				binary.BigEndian.PutUint64(wanted[3:11], 2000)
			case "qos":
				wanted[2] = 0
			case "publisher":
				wanted[16] = 'd'
			case "property":
				wanted[len(wanted)-1] = 'x'
			case "body":
				body = []byte("different")
			case "native":
				wanted = nil
			case "malformed":
				wanted = []byte{1}
			}
			msg := ch.Message{MessageID: 11, MessageSeq: 7, FromUID: "u", ClientMsgNo: "key", Payload: []byte("body"), PublicationMetadata: original, ServerTimestampMS: 1000}
			node := &recordingIdempotencyNode{ok: true, hit: channelstore.IdempotencyHit{Message: msg, PayloadHash: idempotencyTestHash(msg.Payload)}}
			result, found, err := NewChannelIdempotencyStore(node).LookupSend(context.Background(), channelappend.IdempotencyQuery{FromUID: "u", ClientMsgNo: "key", ChannelID: "room", ChannelType: 2, PayloadHash: idempotencyTestHash(msg.Payload), Payload: body, PublicationMetadata: wanted})
			if change == "malformed" {
				require.Error(t, err)
				require.False(t, found)
				return
			}
			require.NoError(t, err)
			require.Equal(t, change == "clock", found)
			if !found {
				require.Zero(t, result.MessageID)
				return
			}
			require.Equal(t, uint64(11), result.MessageID)
			require.Equal(t, uint64(7), result.MessageSeq)
			require.Equal(t, original, node.hit.Message.PublicationMetadata)
			require.Equal(t, len(original)+len(msg.Payload), node.reads[0].Request.MaxBytes)
		})
	}
}

func TestPublicationIdempotencyRejectsChangedCommittedMetadata(t *testing.T) {
	metadata := publicationFixture(t)
	msg := ch.Message{MessageID: 11, MessageSeq: 7, FromUID: "u", ClientMsgNo: "key", Payload: []byte("body"), PublicationMetadata: metadata}
	committed := msg
	committed.PublicationMetadata = bytes.Clone(metadata)
	binary.BigEndian.PutUint64(committed.PublicationMetadata[3:11], 2000)
	node := &recordingIdempotencyNode{ok: true, hit: channelstore.IdempotencyHit{Message: msg}, committed: &committed}
	result, found, err := NewChannelIdempotencyStore(node).LookupSend(context.Background(), channelappend.IdempotencyQuery{FromUID: "u", ClientMsgNo: "key", ChannelID: "room", ChannelType: 2, Payload: msg.Payload, PublicationMetadata: metadata})
	require.NoError(t, err)
	require.False(t, found)
	require.Zero(t, result.MessageID)
}
