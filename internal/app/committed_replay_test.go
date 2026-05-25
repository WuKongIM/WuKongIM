package app

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestCommittedReplayerAdvancesCursorWithoutConversationFlush(t *testing.T) {
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

	require.NoError(t, replayer.RunOnce(context.Background()))

	require.Equal(t, []uint64{1, 2}, delivery.messageSeqs())
	require.Equal(t, []uint64{1, 2}, conversation.messageSeqs())
	require.Equal(t, 0, conversation.flushes)
	require.Equal(t, uint64(2), source.cursors[channel.ChannelKey("channel/1/c1")])
}

func TestCommittedReplaySubmitsDurableCommandMessagesThroughDeliveryOnly(t *testing.T) {
	key := channel.ChannelKey("channel/2/g1____cmd")
	source := &fakeCommittedReplayLog{
		channels: []committedReplayChannel{{
			Key: key,
			ID:  channel.ChannelID{ID: "g1____cmd", Type: frame.ChannelTypeGroup},
		}},
		committed: map[channel.ChannelKey]uint64{key: 1},
		messages: map[channel.ChannelKey][]channel.Message{
			key: {
				{MessageID: 11, MessageSeq: 1, ChannelID: "g1____cmd", ChannelType: frame.ChannelTypeGroup, Payload: []byte("cmd")},
			},
		},
	}
	delivery := &fakeCommittedReplayDelivery{}
	replayer := newCommittedReplayer(committedReplayerConfig{
		Log:       source,
		Delivery:  delivery,
		BatchSize: 16,
	})

	require.NoError(t, replayer.RunOnce(context.Background()))

	require.Len(t, delivery.calls, 1)
	require.Equal(t, uint64(1), delivery.calls[0].MessageSeq)
	require.Equal(t, "g1____cmd", delivery.calls[0].ChannelID)
	require.Equal(t, uint64(1), source.cursors[key])
}

func TestCommittedReplayerAdvancesCursorAfterConversationSubmitAccepted(t *testing.T) {
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
	conversation := &fakeCommittedReplayConversation{}
	replayer := newCommittedReplayer(committedReplayerConfig{
		Log:          source,
		Conversation: conversation,
		BatchSize:    16,
	})

	err := replayer.RunOnce(context.Background())

	require.NoError(t, err)
	require.Equal(t, 0, conversation.flushes)
	require.Equal(t, uint64(1), source.cursors[channel.ChannelKey("channel/1/c1")])
}

func TestCommittedReplayerDoesNotGateCursorOnConversationSubmitError(t *testing.T) {
	key := channel.ChannelKey("channel/1/c1")
	source := &fakeCommittedReplayLog{
		channels: []committedReplayChannel{{
			Key: key,
			ID:  channel.ChannelID{ID: "c1", Type: 1},
		}},
		committed: map[channel.ChannelKey]uint64{key: 1},
		messages: map[channel.ChannelKey][]channel.Message{
			key: {
				{MessageID: 11, MessageSeq: 1, ChannelID: "c1", ChannelType: 1, Payload: []byte("one")},
			},
		},
	}
	conversation := &fakeCommittedReplayConversation{submitErr: errors.New("active hint submit failed")}
	replayer := newCommittedReplayer(committedReplayerConfig{
		Log:          source,
		Conversation: conversation,
		BatchSize:    16,
	})

	require.NoError(t, replayer.RunOnce(context.Background()))
	require.Equal(t, uint64(1), source.cursors[key])
}

func TestCommittedReplayerWithProjectorAdvancesCursorWhenActiveHintStoreBlocks(t *testing.T) {
	key := channel.ChannelKey("channel/1/" + runtimechannelid.EncodePersonChannel("u1", "u2"))
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	source := &fakeCommittedReplayLog{
		channels: []committedReplayChannel{{
			Key: key,
			ID:  channel.ChannelID{ID: channelID, Type: frame.ChannelTypePerson},
		}},
		committed: map[channel.ChannelKey]uint64{key: 1},
		messages: map[channel.ChannelKey][]channel.Message{
			key: {
				{MessageID: 11, MessageSeq: 1, ChannelID: channelID, ChannelType: frame.ChannelTypePerson, Timestamp: 100},
			},
		},
	}
	store := newBlockingProjectorActiveHintStore()
	projector := conversationusecase.NewProjector(conversationusecase.ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
		Async:         func(fn func()) { fn() },
	})
	replayer := newCommittedReplayer(committedReplayerConfig{
		Log:          source,
		Conversation: projector,
		BatchSize:    16,
	})
	defer func() {
		store.Release()
		require.NoError(t, projector.Stop())
	}()

	done := make(chan error, 1)
	go func() { done <- replayer.RunOnce(context.Background()) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(200 * time.Millisecond):
		store.Release()
		require.NoError(t, <-done)
		t.Fatalf("committed replay waited for best-effort active hint flush")
	}
	require.Equal(t, uint64(1), source.cursors[key])
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

func TestCommittedReplayStartsAtMinAvailableSeq(t *testing.T) {
	key := channel.ChannelKey("channel/2/retained")
	source := &fakeCommittedReplayLog{
		channels: []committedReplayChannel{{Key: key, ID: channel.ChannelID{ID: "retained", Type: 2}}},
		cursors:  map[channel.ChannelKey]uint64{key: 2},
		committed: map[channel.ChannelKey]uint64{
			key: 8,
		},
		minAvailable: map[channel.ChannelKey]uint64{
			key: 6,
		},
		messages: map[channel.ChannelKey][]channel.Message{
			key: {
				{MessageID: 16, MessageSeq: 6, ChannelID: "retained", ChannelType: 2, Payload: []byte("six")},
				{MessageID: 17, MessageSeq: 7, ChannelID: "retained", ChannelType: 2, Payload: []byte("seven")},
				{MessageID: 18, MessageSeq: 8, ChannelID: "retained", ChannelType: 2, Payload: []byte("eight")},
			},
		},
	}
	delivery := &fakeCommittedReplayDelivery{}
	conversation := &fakeCommittedReplayConversation{}
	replayer := newCommittedReplayer(committedReplayerConfig{
		Log:          source,
		Delivery:     delivery,
		Conversation: conversation,
		BatchSize:    16,
	})

	require.NoError(t, replayer.RunOnce(context.Background()))

	require.Equal(t, []uint64{6, 7, 8}, delivery.messageSeqs())
	require.Equal(t, []uint64{6, 7, 8}, conversation.messageSeqs())
	require.Equal(t, []uint64{6}, source.loadFromSeqs[key])
	require.Equal(t, []uint64{5}, source.durableAdvances[key])
	require.Equal(t, uint64(8), source.cursors[key])
}

func TestCommittedReplayMissingCursorSkipsRetainedPrefix(t *testing.T) {
	key := channel.ChannelKey("channel/2/missing-retained")
	source := &fakeCommittedReplayLog{
		channels: []committedReplayChannel{{Key: key, ID: channel.ChannelID{ID: "missing-retained", Type: 2}}},
		committed: map[channel.ChannelKey]uint64{
			key: 8,
		},
		minAvailable: map[channel.ChannelKey]uint64{
			key: 6,
		},
		messages: map[channel.ChannelKey][]channel.Message{
			key: {
				{MessageID: 26, MessageSeq: 6, ChannelID: "missing-retained", ChannelType: 2, Payload: []byte("six")},
				{MessageID: 27, MessageSeq: 7, ChannelID: "missing-retained", ChannelType: 2, Payload: []byte("seven")},
				{MessageID: 28, MessageSeq: 8, ChannelID: "missing-retained", ChannelType: 2, Payload: []byte("eight")},
			},
		},
	}
	replayer := newCommittedReplayer(committedReplayerConfig{
		Log:       source,
		BatchSize: 16,
	})

	require.NoError(t, replayer.RunOnce(context.Background()))

	require.Equal(t, []uint64{6}, source.loadFromSeqs[key])
	require.Equal(t, []uint64{5}, source.durableAdvances[key])
	require.Equal(t, uint64(8), source.cursors[key])
}

func TestCommittedReplayMaxCursorDoesNotRegressToRetentionFloor(t *testing.T) {
	key := channel.ChannelKey("channel/2/max-cursor")
	maxCursor := ^uint64(0)
	source := &fakeCommittedReplayLog{
		channels: []committedReplayChannel{{Key: key, ID: channel.ChannelID{ID: "max-cursor", Type: 2}}},
		cursors:  map[channel.ChannelKey]uint64{key: maxCursor},
		committed: map[channel.ChannelKey]uint64{
			key: maxCursor,
		},
	}
	replayer := newCommittedReplayer(committedReplayerConfig{
		Log:       source,
		BatchSize: 16,
	})

	require.NoError(t, replayer.RunOnce(context.Background()))

	require.Empty(t, source.loadFromSeqs[key])
	require.Empty(t, source.durableAdvances[key])
	require.Equal(t, maxCursor, source.cursors[key])
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

	require.Equal(t, []string{"group:0"}, metrics.lags)
	require.Equal(t, uint64(5), source.cursors[key])
}

func TestCommittedReplayerRecordsMaxReplayLagByChannelType(t *testing.T) {
	laggingKey := channel.ChannelKey("channel/2/lagging")
	caughtUpKey := channel.ChannelKey("channel/2/caught-up")
	metrics := &recordingCommittedReplayMetrics{}
	source := &fakeCommittedReplayLog{
		channels: []committedReplayChannel{
			{Key: laggingKey, ID: channel.ChannelID{ID: "lagging", Type: 2}},
			{Key: caughtUpKey, ID: channel.ChannelID{ID: "caught-up", Type: 2}},
		},
		cursors: map[channel.ChannelKey]uint64{
			laggingKey: 2,
		},
		committed: map[channel.ChannelKey]uint64{
			laggingKey:  5,
			caughtUpKey: 1,
		},
		messages: map[channel.ChannelKey][]channel.Message{
			caughtUpKey: {
				{MessageID: 16, MessageSeq: 1, ChannelID: "caught-up", ChannelType: 2, Payload: []byte("one")},
			},
		},
	}
	replayer := newCommittedReplayer(committedReplayerConfig{
		Log:       source,
		BatchSize: 16,
		Metrics:   metrics,
	})

	require.NoError(t, replayer.RunOnce(context.Background()))

	require.Equal(t, []string{"group:3"}, metrics.lags)
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

func TestCommittedReplayerUsesDirtyChannelsWithoutFullListing(t *testing.T) {
	id := channel.ChannelID{ID: "dirty", Type: frame.ChannelTypeGroup}
	key := channelhandler.KeyFromChannelID(id)
	source := &fakeCommittedReplayLog{
		listErr: errors.New("full committed replay scan should not run"),
		committed: map[channel.ChannelKey]uint64{
			key: 1,
		},
		messages: map[channel.ChannelKey][]channel.Message{
			key: {
				{MessageID: 41, MessageSeq: 1, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("dirty")},
			},
		},
	}
	delivery := &fakeCommittedReplayDelivery{}
	replayer := newCommittedReplayer(committedReplayerConfig{
		Log:       source,
		Delivery:  delivery,
		BatchSize: 16,
	})

	require.NoError(t, replayer.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		Message: channel.Message{ChannelID: id.ID, ChannelType: id.Type, MessageSeq: 1},
	}))
	require.NoError(t, replayer.RunOnce(context.Background()))

	require.Zero(t, source.listCalls)
	require.Equal(t, []uint64{1}, delivery.messageSeqs())
	require.Equal(t, uint64(1), source.cursors[key])
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
	channels        []committedReplayChannel
	cursors         map[channel.ChannelKey]uint64
	committed       map[channel.ChannelKey]uint64
	minAvailable    map[channel.ChannelKey]uint64
	messages        map[channel.ChannelKey][]channel.Message
	loadFromSeqs    map[channel.ChannelKey][]uint64
	durableAdvances map[channel.ChannelKey][]uint64
	listErr         error
	listCalls       int
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

func (f *blockingCommittedReplayLog) LoadCommittedMessages(context.Context, channel.ChannelKey, channel.ChannelID, uint64, int, int) ([]channel.Message, error) {
	return nil, nil
}

func (f *blockingCommittedReplayLog) AdvanceCommittedDispatchCursorDurable(context.Context, channel.ChannelKey, string, uint64) error {
	return nil
}

func (f *blockingCommittedReplayLog) CommittedReplayState(context.Context, channel.ChannelKey, channel.ChannelID) (committedReplayChannelState, error) {
	return committedReplayChannelState{}, nil
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
	f.listCalls++
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
	if f.cursors == nil {
		f.cursors = make(map[channel.ChannelKey]uint64)
	}
	f.cursors[key] = seq
	return nil
}

func (f *fakeCommittedReplayLog) AdvanceCommittedDispatchCursorDurable(_ context.Context, key channel.ChannelKey, _ string, seq uint64) error {
	if f.cursors == nil {
		f.cursors = make(map[channel.ChannelKey]uint64)
	}
	if f.durableAdvances == nil {
		f.durableAdvances = make(map[channel.ChannelKey][]uint64)
	}
	f.durableAdvances[key] = append(f.durableAdvances[key], seq)
	f.cursors[key] = seq
	return nil
}

func (f *fakeCommittedReplayLog) CommittedReplayState(_ context.Context, key channel.ChannelKey, _ channel.ChannelID) (committedReplayChannelState, error) {
	return committedReplayChannelState{
		CommittedSeq:    f.committed[key],
		MinAvailableSeq: f.minAvailable[key],
	}, nil
}

func (f *fakeCommittedReplayLog) LoadCommittedMessages(_ context.Context, key channel.ChannelKey, _ channel.ChannelID, fromSeq uint64, limit int, _ int) ([]channel.Message, error) {
	if f.loadFromSeqs == nil {
		f.loadFromSeqs = make(map[channel.ChannelKey][]uint64)
	}
	f.loadFromSeqs[key] = append(f.loadFromSeqs[key], fromSeq)
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
	calls     []channel.Message
	flushes   int
	submitErr error
}

type blockingProjectorActiveHintStore struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingProjectorActiveHintStore() *blockingProjectorActiveHintStore {
	return &blockingProjectorActiveHintStore{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *blockingProjectorActiveHintStore) SubmitUserConversationActiveHints(context.Context, []metadb.UserConversationActiveHint) error {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return nil
}

func (s *blockingProjectorActiveHintStore) ListChannelSubscribers(context.Context, string, int64, string, int) ([]string, string, bool, error) {
	return nil, "", true, nil
}

func (s *blockingProjectorActiveHintStore) Release() {
	select {
	case <-s.release:
	default:
		close(s.release)
	}
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
	if f.submitErr != nil {
		return f.submitErr
	}
	f.calls = append(f.calls, msg)
	return nil
}

func (f *fakeCommittedReplayConversation) messageSeqs() []uint64 {
	seqs := make([]uint64, 0, len(f.calls))
	for _, call := range f.calls {
		seqs = append(seqs, call.MessageSeq)
	}
	return seqs
}
