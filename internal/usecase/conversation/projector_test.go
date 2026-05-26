package conversation

import (
	"context"
	"sync"
	"testing"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestProjectorSubmitsPersonActiveHints(t *testing.T) {
	store := newProjectorStoreStub()
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	projector := NewProjector(ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
		Async:         func(fn func()) { fn() },
	})

	msg := channel.Message{
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		MessageSeq:  42,
		Timestamp:   100,
	}
	require.NoError(t, projector.SubmitCommitted(context.Background(), msg))
	require.NoError(t, projector.Flush(context.Background()))

	activeAt := time.Unix(int64(msg.Timestamp), 0).UnixNano()
	require.ElementsMatch(t, []metadb.UserConversationActiveHint{
		{UID: "u1", ChannelID: channelID, ChannelType: int64(frame.ChannelTypePerson), ActiveAt: activeAt, MessageSeq: 42},
		{UID: "u2", ChannelID: channelID, ChannelType: int64(frame.ChannelTypePerson), ActiveAt: activeAt, MessageSeq: 42},
	}, store.submittedHints())
}

func TestProjectorSubmitCommittedDoesNotBlockOnSlowActiveHintRouting(t *testing.T) {
	store := newProjectorStoreStub()
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	asyncFns := make(chan func(), 1)
	projector := NewProjector(ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
		Async: func(fn func()) {
			asyncFns <- fn
		},
	})

	require.NoError(t, projector.SubmitCommitted(context.Background(), channel.Message{
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		MessageSeq:  1,
		Timestamp:   100,
	}))

	require.Zero(t, store.submitCalls())
	fn := requireScheduledFlush(t, asyncFns)
	fn()
	require.Equal(t, 1, store.submitCalls())
}

func TestProjectorSubmitCommittedNeverRoutesActiveHintsInline(t *testing.T) {
	store := newProjectorStoreStub()
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	store.beforeSubmit = func(context.Context) {
		once.Do(func() { close(started) })
		<-release
	}
	projector := NewProjector(ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
		Async:         func(fn func()) { fn() },
	})

	done := make(chan error, 1)
	go func() {
		done <- projector.SubmitCommitted(context.Background(), channel.Message{
			ChannelID:   channelID,
			ChannelType: frame.ChannelTypePerson,
			MessageSeq:  1,
			Timestamp:   100,
		})
	}()

	select {
	case err := <-done:
		close(release)
		require.NoError(t, err)
	case <-started:
		close(release)
		require.NoError(t, <-done)
		t.Fatalf("SubmitCommitted routed active hints inline")
	case <-time.After(200 * time.Millisecond):
		close(release)
		t.Fatalf("SubmitCommitted did not return")
	}
}

func TestProjectorSubmitCommittedIgnoresCommandChannels(t *testing.T) {
	store := newProjectorStoreStub()
	asyncFns := make(chan func(), 1)
	projector := NewProjector(ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
		Async: func(fn func()) {
			asyncFns <- fn
		},
	})

	require.NoError(t, projector.SubmitCommitted(context.Background(), channel.Message{
		ChannelID:   runtimechannelid.ToCommandChannel("u1|u2"),
		ChannelType: frame.ChannelTypePerson,
		MessageSeq:  8,
		Timestamp:   100,
	}))

	select {
	case fn := <-asyncFns:
		fn()
		t.Fatalf("SubmitCommitted scheduled a flush for command channel messages")
	case <-time.After(50 * time.Millisecond):
	}
	require.NoError(t, projector.Flush(context.Background()))
	require.Empty(t, store.submittedHints())
}

func TestProjectorSubmitCommittedIgnoresSyncOnceMessages(t *testing.T) {
	store := newProjectorStoreStub()
	asyncFns := make(chan func(), 1)
	projector := NewProjector(ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
		Async: func(fn func()) {
			asyncFns <- fn
		},
	})

	require.NoError(t, projector.SubmitCommitted(context.Background(), channel.Message{
		ChannelID:   runtimechannelid.EncodePersonChannel("u1", "u2"),
		ChannelType: frame.ChannelTypePerson,
		Framer:      frame.Framer{SyncOnce: true},
		MessageSeq:  9,
		Timestamp:   100,
	}))

	select {
	case fn := <-asyncFns:
		fn()
		t.Fatalf("SubmitCommitted scheduled a flush for SyncOnce messages")
	case <-time.After(50 * time.Millisecond):
	}
	require.NoError(t, projector.Flush(context.Background()))
	require.Empty(t, store.submittedHints())
}

func TestProjectorStopCancelsScheduledFlush(t *testing.T) {
	store := newProjectorStoreStub()
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	started := make(chan struct{})
	store.beforeSubmit = func(ctx context.Context) {
		close(started)
		<-ctx.Done()
	}
	projector := NewProjector(ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
	})
	require.NoError(t, projector.Start())

	require.NoError(t, projector.SubmitCommitted(context.Background(), channel.Message{
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		MessageSeq:  1,
		Timestamp:   100,
	}))
	<-started

	done := make(chan error, 1)
	go func() { done <- projector.Stop() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatalf("Stop did not cancel scheduled active hint flush")
	}
}

func TestProjectorOnlySubmitsActiveHints(t *testing.T) {
	store := newProjectorStoreStub()
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	projector := NewProjector(ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
		Async:         func(func()) {},
	})

	require.NoError(t, projector.SubmitCommitted(context.Background(), channel.Message{
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		MessageSeq:  7,
		Timestamp:   100,
	}))
	require.NoError(t, projector.Flush(context.Background()))
	require.Len(t, store.submittedHints(), 2)
}

func TestProjectorThrottlesGroupActiveFanout(t *testing.T) {
	store := newProjectorStoreStub()
	store.subscribers[metadb.ConversationKey{ChannelID: "g1", ChannelType: 2}] = []string{"u1", "u2", "u3"}
	now := time.Unix(100, 0)
	projector := NewProjector(ProjectorOptions{
		Store:                           store,
		FlushInterval:                   time.Hour,
		SubscriberPageSize:              2,
		GroupActiveFanoutInterval:       time.Hour,
		GroupActiveFanoutMaxSubscribers: 10,
		Now:                             func() time.Time { return now },
		Async:                           func(fn func()) { fn() },
	})

	require.NoError(t, projector.SubmitCommitted(context.Background(), groupMessage("g1", 10, int32(now.Unix()))))
	require.NoError(t, projector.Flush(context.Background()))
	now = now.Add(30 * time.Minute)
	require.NoError(t, projector.SubmitCommitted(context.Background(), groupMessage("g1", 11, int32(now.Unix()))))
	require.NoError(t, projector.Flush(context.Background()))
	now = now.Add(90 * time.Minute)
	require.NoError(t, projector.SubmitCommitted(context.Background(), groupMessage("g1", 12, int32(now.Unix()))))
	require.NoError(t, projector.Flush(context.Background()))

	require.Equal(t, []subscriberListCall{
		{ChannelID: "g1", ChannelType: 2, AfterUID: "", Limit: 2},
		{ChannelID: "g1", ChannelType: 2, AfterUID: "u2", Limit: 2},
		{ChannelID: "g1", ChannelType: 2, AfterUID: "", Limit: 2},
		{ChannelID: "g1", ChannelType: 2, AfterUID: "u2", Limit: 2},
	}, store.subscriberCallsSnapshot())

	hints := store.submittedHints()
	require.Len(t, hints, 6)
	for _, hint := range hints[:3] {
		require.Equal(t, uint64(10), hint.MessageSeq)
	}
	for _, hint := range hints[3:] {
		require.Equal(t, uint64(12), hint.MessageSeq)
	}
}

func TestProjectorDropsGroupFanoutWhenSubscriberBudgetExceeded(t *testing.T) {
	store := newProjectorStoreStub()
	store.subscribers[metadb.ConversationKey{ChannelID: "g1", ChannelType: 2}] = []string{"u1", "u2", "u3", "u4", "u5"}
	projector := NewProjector(ProjectorOptions{
		Store:                           store,
		FlushInterval:                   time.Hour,
		SubscriberPageSize:              2,
		GroupActiveFanoutInterval:       time.Hour,
		GroupActiveFanoutMaxSubscribers: 3,
		Now:                             func() time.Time { return time.Unix(100, 0) },
		Async:                           func(fn func()) { fn() },
	})

	require.NoError(t, projector.SubmitCommitted(context.Background(), groupMessage("g1", 10, 100)))
	require.NoError(t, projector.Flush(context.Background()))

	hints := store.submittedHints()
	require.Len(t, hints, 3)
	require.ElementsMatch(t, []string{"u1", "u2", "u3"}, hintUIDs(hints))
	require.Equal(t, []subscriberListCall{
		{ChannelID: "g1", ChannelType: 2, AfterUID: "", Limit: 2},
		{ChannelID: "g1", ChannelType: 2, AfterUID: "u2", Limit: 1},
	}, store.subscriberCallsSnapshot())
}

func TestProjectorBoundsGroupFanoutThrottleState(t *testing.T) {
	projector := NewProjector(ProjectorOptions{
		ActiveHintQueueSize: 2,
		Now:                 func() time.Time { return time.Unix(100, 0) },
	}).(*projector)

	for i := 0; i < 5; i++ {
		key := metadb.ConversationKey{ChannelID: string(rune('a' + i)), ChannelType: 2}
		require.True(t, projector.allowGroupFanout(key, time.Unix(int64(100+i), 0)))
	}
	require.LessOrEqual(t, len(projector.lastGroupFanoutAt), 2)
}

func requireScheduledFlush(t *testing.T, scheduled <-chan func()) func() {
	t.Helper()
	select {
	case fn := <-scheduled:
		return fn
	case <-time.After(time.Second):
		t.Fatalf("scheduled flush was not queued")
	}
	return nil
}

func groupMessage(channelID string, seq uint64, ts int32) channel.Message {
	return channel.Message{ChannelID: channelID, ChannelType: frame.ChannelTypeGroup, MessageSeq: seq, Timestamp: ts}
}

func hintUIDs(hints []metadb.UserConversationActiveHint) []string {
	uids := make([]string, 0, len(hints))
	for _, hint := range hints {
		uids = append(uids, hint.UID)
	}
	return uids
}

type projectorStoreStub struct {
	mu              sync.Mutex
	hints           []metadb.UserConversationActiveHint
	submitCount     int
	beforeSubmit    func(context.Context)
	subscribers     map[metadb.ConversationKey][]string
	subscriberCalls []subscriberListCall
}

type subscriberListCall struct {
	ChannelID   string
	ChannelType int64
	AfterUID    string
	Limit       int
}

func newProjectorStoreStub() *projectorStoreStub {
	return &projectorStoreStub{subscribers: make(map[metadb.ConversationKey][]string)}
}

func (s *projectorStoreStub) SubmitUserConversationActiveHints(ctx context.Context, hints []metadb.UserConversationActiveHint) error {
	s.mu.Lock()
	s.submitCount++
	hook := s.beforeSubmit
	s.mu.Unlock()

	if hook != nil {
		hook(ctx)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.hints = append(s.hints, hints...)
	return nil
}

func (s *projectorStoreStub) ListChannelSubscribers(_ context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	s.mu.Lock()
	s.subscriberCalls = append(s.subscriberCalls, subscriberListCall{
		ChannelID: channelID, ChannelType: channelType, AfterUID: afterUID, Limit: limit,
	})
	uids := append([]string(nil), s.subscribers[metadb.ConversationKey{ChannelID: channelID, ChannelType: channelType}]...)
	s.mu.Unlock()

	start := 0
	if afterUID != "" {
		for i, uid := range uids {
			if uid == afterUID {
				start = i + 1
				break
			}
		}
	}
	if start >= len(uids) {
		return nil, afterUID, true, nil
	}
	end := start + limit
	if end > len(uids) {
		end = len(uids)
	}
	page := append([]string(nil), uids[start:end]...)
	next := afterUID
	if len(page) > 0 {
		next = page[len(page)-1]
	}
	return page, next, end == len(uids), nil
}

func (s *projectorStoreStub) submittedHints() []metadb.UserConversationActiveHint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]metadb.UserConversationActiveHint(nil), s.hints...)
}

func (s *projectorStoreStub) submitCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.submitCount
}

func (s *projectorStoreStub) subscriberCallsSnapshot() []subscriberListCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]subscriberListCall(nil), s.subscriberCalls...)
}
