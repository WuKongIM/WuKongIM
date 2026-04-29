package app

import (
	"context"
	"errors"
	"testing"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestCommittedReplayerFlushesConversationBeforeAdvancingCursor(t *testing.T) {
	source := &fakeCommittedReplayLog{
		channels: []committedReplayChannel{{
			Key: channel.ChannelKey("channel/1/c1"),
			ID:  channel.ChannelID{ID: "c1", Type: 1},
		}},
		committed: map[channel.ChannelKey]uint64{
			channel.ChannelKey("channel/1/c1"): 2,
		},
		messages: map[channel.ChannelKey][]channel.Message{
			channel.ChannelKey("channel/1/c1"): {
				{MessageID: 11, MessageSeq: 1, ChannelID: "c1", ChannelType: 1, Payload: []byte("one")},
				{MessageID: 12, MessageSeq: 2, ChannelID: "c1", ChannelType: 1, Payload: []byte("two")},
			},
		},
	}
	conversation := &fakeCommittedReplayConversation{}
	delivery := &fakeCommittedReplayDelivery{}
	replayer := newCommittedReplayer(committedReplayerConfig{
		Log:          source,
		Delivery:     delivery,
		Conversation: conversation,
		BatchSize:    16,
	})
	source.canStoreCursor = func() bool { return conversation.flushes == 1 }

	require.NoError(t, replayer.RunOnce(context.Background()))

	require.Equal(t, []uint64{1, 2}, delivery.messageSeqs())
	require.Equal(t, []uint64{1, 2}, conversation.messageSeqs())
	require.Equal(t, 1, conversation.flushes)
	require.Equal(t, uint64(2), source.cursors[channel.ChannelKey("channel/1/c1")])
}

func TestCommittedReplayerDoesNotAdvanceCursorWhenConversationFlushFails(t *testing.T) {
	source := &fakeCommittedReplayLog{
		channels: []committedReplayChannel{{
			Key: channel.ChannelKey("channel/1/c1"),
			ID:  channel.ChannelID{ID: "c1", Type: 1},
		}},
		committed: map[channel.ChannelKey]uint64{
			channel.ChannelKey("channel/1/c1"): 1,
		},
		messages: map[channel.ChannelKey][]channel.Message{
			channel.ChannelKey("channel/1/c1"): {
				{MessageID: 11, MessageSeq: 1, ChannelID: "c1", ChannelType: 1, Payload: []byte("one")},
			},
		},
	}
	conversation := &fakeCommittedReplayConversation{flushErr: errors.New("flush failed")}
	replayer := newCommittedReplayer(committedReplayerConfig{
		Log:          source,
		Conversation: conversation,
		BatchSize:    16,
	})

	err := replayer.RunOnce(context.Background())

	require.ErrorIs(t, err, conversation.flushErr)
	require.Empty(t, source.cursors)
}

func TestCommittedReplayerDoesNotAdvanceAcrossMessageSeqGap(t *testing.T) {
	key := channel.ChannelKey("channel/1/c1")
	source := &fakeCommittedReplayLog{
		channels: []committedReplayChannel{{Key: key, ID: channel.ChannelID{ID: "c1", Type: 1}}},
		committed: map[channel.ChannelKey]uint64{
			key: 2,
		},
		messages: map[channel.ChannelKey][]channel.Message{
			key: {
				{MessageID: 12, MessageSeq: 2, ChannelID: "c1", ChannelType: 1, Payload: []byte("two")},
			},
		},
	}
	replayer := newCommittedReplayer(committedReplayerConfig{
		Log:       source,
		BatchSize: 16,
	})

	err := replayer.RunOnce(context.Background())

	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.Empty(t, source.cursors)
}

type fakeCommittedReplayLog struct {
	channels       []committedReplayChannel
	cursors        map[channel.ChannelKey]uint64
	committed      map[channel.ChannelKey]uint64
	messages       map[channel.ChannelKey][]channel.Message
	canStoreCursor func() bool
}

func (f *fakeCommittedReplayLog) ListCommittedReplayChannels(context.Context) ([]committedReplayChannel, error) {
	return append([]committedReplayChannel(nil), f.channels...), nil
}

func (f *fakeCommittedReplayLog) LoadCommittedDispatchCursor(_ context.Context, key channel.ChannelKey, _ string) (uint64, bool, error) {
	if f.cursors == nil {
		return 0, false, nil
	}
	seq, ok := f.cursors[key]
	return seq, ok, nil
}

func (f *fakeCommittedReplayLog) StoreCommittedDispatchCursor(_ context.Context, key channel.ChannelKey, _ string, seq uint64) error {
	if f.canStoreCursor != nil && !f.canStoreCursor() {
		return errors.New("cursor stored before flush")
	}
	if f.cursors == nil {
		f.cursors = make(map[channel.ChannelKey]uint64)
	}
	f.cursors[key] = seq
	return nil
}

func (f *fakeCommittedReplayLog) CommittedSeq(_ context.Context, key channel.ChannelKey, _ channel.ChannelID) (uint64, error) {
	return f.committed[key], nil
}

func (f *fakeCommittedReplayLog) LoadCommittedMessages(_ context.Context, key channel.ChannelKey, _ channel.ChannelID, fromSeq uint64, limit int, _ int) ([]channel.Message, error) {
	var out []channel.Message
	for _, msg := range f.messages[key] {
		if msg.MessageSeq < fromSeq {
			continue
		}
		out = append(out, msg)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

type fakeCommittedReplayDelivery struct {
	calls []deliveryruntime.CommittedEnvelope
}

func (f *fakeCommittedReplayDelivery) SubmitCommitted(_ context.Context, env deliveryruntime.CommittedEnvelope) error {
	f.calls = append(f.calls, env)
	return nil
}

func (f *fakeCommittedReplayDelivery) messageSeqs() []uint64 {
	seqs := make([]uint64, 0, len(f.calls))
	for _, call := range f.calls {
		seqs = append(seqs, call.MessageSeq)
	}
	return seqs
}

type fakeCommittedReplayConversation struct {
	calls    []channel.Message
	flushes  int
	flushErr error
}

func (f *fakeCommittedReplayConversation) SubmitCommitted(_ context.Context, msg channel.Message) error {
	f.calls = append(f.calls, msg)
	return nil
}

func (f *fakeCommittedReplayConversation) Flush(context.Context) error {
	f.flushes++
	return f.flushErr
}

func (f *fakeCommittedReplayConversation) messageSeqs() []uint64 {
	seqs := make([]uint64, 0, len(f.calls))
	for _, call := range f.calls {
		seqs = append(seqs, call.MessageSeq)
	}
	return seqs
}
