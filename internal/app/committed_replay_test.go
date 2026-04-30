package app

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

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

func TestCommittedReplayerRecordsReplayLag(t *testing.T) {
	key := channel.ChannelKey("channel/2/g1")
	metrics := &recordingCommittedReplayMetrics{}
	source := &fakeCommittedReplayLog{
		channels: []committedReplayChannel{{Key: key, ID: channel.ChannelID{ID: "g1", Type: 2}}},
		cursors:  map[channel.ChannelKey]uint64{key: 2},
		committed: map[channel.ChannelKey]uint64{
			key: 5,
		},
		messages: map[channel.ChannelKey][]channel.Message{
			key: {
				{MessageID: 13, MessageSeq: 3, ChannelID: "g1", ChannelType: 2, Payload: []byte("three")},
				{MessageID: 14, MessageSeq: 4, ChannelID: "g1", ChannelType: 2, Payload: []byte("four")},
				{MessageID: 15, MessageSeq: 5, ChannelID: "g1", ChannelType: 2, Payload: []byte("five")},
			},
		},
	}
	replayer := newCommittedReplayer(committedReplayerConfig{
		Log:       source,
		BatchSize: 16,
		Metrics:   metrics,
	})

	require.NoError(t, replayer.RunOnce(context.Background()))

	require.Equal(t, []string{"group:3", "group:0"}, metrics.lags)
	require.Equal(t, uint64(5), source.cursors[key])
}

func TestCommittedReplayerRecordsPassDuration(t *testing.T) {
	metrics := &recordingCommittedReplayMetrics{}
	replayer := newCommittedReplayer(committedReplayerConfig{
		Log:     &fakeCommittedReplayLog{},
		Metrics: metrics,
	})
	require.NoError(t, replayer.RunOnce(context.Background()))

	errMetrics := &recordingCommittedReplayMetrics{}
	wantErr := errors.New("list failed")
	errReplayer := newCommittedReplayer(committedReplayerConfig{
		Log:     &fakeCommittedReplayLog{listErr: wantErr},
		Metrics: errMetrics,
	})
	err := errReplayer.RunOnce(context.Background())

	require.ErrorIs(t, err, wantErr)
	require.Equal(t, []string{"ok"}, metrics.passes)
	require.Equal(t, []string{"error"}, errMetrics.passes)
}

func TestCommittedReplayerStopContextBoundsBlockedPass(t *testing.T) {
	source := newBlockingCommittedReplayLog()
	replayer := newCommittedReplayer(committedReplayerConfig{
		Log:      source,
		Interval: time.Hour,
	})
	require.NoError(t, replayer.Start(context.Background()))
	source.WaitEntered(t)

	firstCtx, firstCancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer firstCancel()
	require.ErrorIs(t, replayer.StopContext(firstCtx), context.DeadlineExceeded)

	secondCtx, secondCancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer secondCancel()
	require.ErrorIs(t, replayer.StopContext(secondCtx), context.DeadlineExceeded)

	source.Release()
	require.Eventually(t, func() bool { return replayerStoppedForTest(replayer) }, time.Second, time.Millisecond)
	require.NoError(t, replayer.StopContext(context.Background()))
}

type fakeCommittedReplayLog struct {
	channels       []committedReplayChannel
	cursors        map[channel.ChannelKey]uint64
	committed      map[channel.ChannelKey]uint64
	messages       map[channel.ChannelKey][]channel.Message
	canStoreCursor func() bool
	listErr        error
}

type blockingCommittedReplayLog struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingCommittedReplayLog() *blockingCommittedReplayLog {
	return &blockingCommittedReplayLog{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (f *blockingCommittedReplayLog) ListCommittedReplayChannels(context.Context) ([]committedReplayChannel, error) {
	f.once.Do(func() { close(f.entered) })
	<-f.release
	return nil, nil
}

func (f *blockingCommittedReplayLog) LoadCommittedDispatchCursor(context.Context, channel.ChannelKey, string) (uint64, bool, error) {
	return 0, false, nil
}

func (f *blockingCommittedReplayLog) StoreCommittedDispatchCursor(context.Context, channel.ChannelKey, string, uint64) error {
	return nil
}

func (f *blockingCommittedReplayLog) CommittedSeq(context.Context, channel.ChannelKey, channel.ChannelID) (uint64, error) {
	return 0, nil
}

func (f *blockingCommittedReplayLog) LoadCommittedMessages(context.Context, channel.ChannelKey, channel.ChannelID, uint64, int, int) ([]channel.Message, error) {
	return nil, nil
}

func (f *blockingCommittedReplayLog) WaitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		require.FailNow(t, "timed out waiting for committed replay log")
	}
}

func (f *blockingCommittedReplayLog) Release() {
	close(f.release)
}

func replayerStoppedForTest(r *committedReplayer) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cancel == nil && !r.stopping && r.done == nil
}

func (f *fakeCommittedReplayLog) ListCommittedReplayChannels(context.Context) ([]committedReplayChannel, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
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

type recordingCommittedReplayMetrics struct {
	lags   []string
	passes []string
}

func (m *recordingCommittedReplayMetrics) SetCommittedReplayLag(channelType string, lag uint64) {
	m.lags = append(m.lags, channelType+":"+strconv.FormatUint(lag, 10))
}

func (m *recordingCommittedReplayMetrics) ObserveCommittedReplayPass(result string, _ time.Duration) {
	m.passes = append(m.passes, result)
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
