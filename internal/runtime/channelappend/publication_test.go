package channelappend

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/stretchr/testify/require"
)

func publicationFixture(t *testing.T) []byte {
	t.Helper()
	b, err := hex.DecodeString("01010100000000000003e800016e00016300017400030600017800036f6e65020000003c06000178000374776f")
	require.NoError(t, err)
	return b
}

func TestPublicationAppendAndEnvelopesPreserveOriginalMetadata(t *testing.T) {
	metadata := publicationFixture(t)
	cmd := SendCommand{FromUID: "u", ClientMsgNo: "key", ChannelID: "room", ChannelType: 2, Payload: []byte("body"), PublicationMetadata: bytes.Clone(metadata), MessageID: 11}
	item := preparedSend{Command: cmd, ServerTimestampMS: 2000}
	request := appendRequest(localTargetForAppendTest("room"), []preparedSend{item}, 1)
	require.Equal(t, metadata, request.Messages[0].PublicationMetadata)
	for _, fromAppend := range []bool{false, true} {
		saved := bytes.Clone(metadata)
		binary.BigEndian.PutUint64(saved[3:11], 500)
		appended := AppendBatchItemResult{MessageID: 11, MessageSeq: 1}
		if fromAppend {
			appended.Message.PublicationMetadata = saved
		}
		event := committedEnvelopeForAppend(item, appended)
		want := metadata
		if fromAppend {
			want = saved
		}
		require.Equal(t, want, event.PublicationMetadata)
		clear(event.PublicationMetadata)
		require.Equal(t, metadata, item.Command.PublicationMetadata)
		if fromAppend {
			require.Equal(t, uint64(500), binary.BigEndian.Uint64(saved[3:11]))
		}
	}
	transient := committedEnvelopeForRealtime(item)
	require.Equal(t, metadata, transient.PublicationMetadata)
	clear(transient.PublicationMetadata)
	require.Equal(t, metadata, item.Command.PublicationMetadata)
	lookup := &recordingIdempotencyForPrepare{}
	_, _, err := lookupIdempotentSend(context.Background(), cmd, preparePorts{idempotency: lookup})
	require.NoError(t, err)
	query := lookup.queriesSnapshot()[0]
	require.Equal(t, metadata, query.PublicationMetadata)
	require.Equal(t, cmd.Payload, query.Payload)
}

func TestPublicationCoalescingIgnoresOnlyIngressClock(t *testing.T) {
	metadata := publicationFixture(t)
	first := preparedSend{Command: SendCommand{FromUID: "u", ClientMsgNo: "key", Payload: []byte("body"), PublicationMetadata: metadata}}
	for _, count := range []int{2, 129} {
		for _, changed := range []bool{false, true} {
			items := make([]preparedSend, count)
			for i := range items {
				items[i] = first
				items[i].Command.PublicationMetadata = bytes.Clone(metadata)
				binary.BigEndian.PutUint64(items[i].Command.PublicationMetadata[3:11], uint64(2000+i))
			}
			items[0] = first
			if changed {
				items[count-1].Command.PublicationMetadata[2] = 0
			}
			batch := newIdempotentAppendBatch(items)
			want := 1
			if changed {
				want = 2
			}
			require.Len(t, batch.items, want)
			require.Equal(t, metadata, batch.items[0].Command.PublicationMetadata)
			unique := make([]appendItemCompletion, len(batch.items))
			for i := range unique {
				unique[i].committed = true
			}
			effects := 0
			for _, c := range batch.expandCompletions(unique) {
				if c.committed {
					effects++
				}
			}
			require.Equal(t, want, effects)
		}
	}
}

func TestPublicationInvalidMetadataRejectedBeforeSendPreparation(t *testing.T) {
	for _, transient := range []bool{false, true} {
		cmd := SendCommand{FromUID: "u", ChannelID: "room", ChannelType: 2, Payload: []byte("body"), PublicationMetadata: []byte{1}, NoPersist: transient}
		_, r, done := preRouteChannel(cmd, channelid.CommandCodec{})
		require.True(t, done)
		require.Equal(t, ReasonInvalidRequest, r.Result.Reason)
		result, done := prepareSend(context.Background(), cmd, preparePorts{}, true)
		require.True(t, done)
		require.Equal(t, ReasonInvalidRequest, result.result.Reason)
	}
}
