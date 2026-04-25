package conversation

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestProjectorCoalescesMultipleMessagesIntoSingleFlushEntry(t *testing.T) {
	store := newProjectorStoreStub()
	projector := NewProjector(ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
		DirtyLimit:    8,
		ColdThreshold: 30 * 24 * time.Hour,
		Now:           func() time.Time { return time.Unix(100, 0) },
		Async:         func(fn func()) { fn() },
	})

	msg1 := channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 10, ClientMsgNo: "c1", Timestamp: 100}
	msg2 := channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 11, ClientMsgNo: "c2", Timestamp: 101}
	require.NoError(t, projector.SubmitCommitted(context.Background(), msg1))
	require.NoError(t, projector.SubmitCommitted(context.Background(), msg2))
	require.NoError(t, projector.Flush(context.Background()))

	require.Len(t, store.updates, 1)
	require.Equal(t, metadb.ChannelUpdateLog{
		ChannelID:       "g1",
		ChannelType:     2,
		UpdatedAt:       time.Unix(101, 0).UnixNano(),
		LastMsgSeq:      11,
		LastClientMsgNo: "c2",
		LastMsgAt:       time.Unix(101, 0).UnixNano(),
	}, store.updates[0])
}

func TestProjectorColdWakeupTouchesPersonConversationStates(t *testing.T) {
	now := time.Unix(40*24*60*60, 0)
	store := newProjectorStoreStub()
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	store.cold[metadb.ConversationKey{ChannelID: channelID, ChannelType: int64(frame.ChannelTypePerson)}] = metadb.ChannelUpdateLog{
		ChannelID:   channelID,
		ChannelType: int64(frame.ChannelTypePerson),
		LastMsgAt:   now.Add(-31 * 24 * time.Hour).UnixNano(),
	}

	projector := NewProjector(ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
		DirtyLimit:    8,
		ColdThreshold: 30 * 24 * time.Hour,
		Now:           func() time.Time { return now },
		Async:         func(fn func()) { fn() },
	})

	msg := channel.Message{
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		MessageSeq:  10,
		ClientMsgNo: "c1",
		Timestamp:   int32(now.Unix()),
	}
	require.NoError(t, projector.SubmitCommitted(context.Background(), msg))
	require.ElementsMatch(t, []metadb.UserConversationActivePatch{
		{UID: "u1", ChannelID: channelID, ChannelType: int64(frame.ChannelTypePerson), ActiveAt: time.Unix(int64(msg.Timestamp), 0).UnixNano()},
		{UID: "u2", ChannelID: channelID, ChannelType: int64(frame.ChannelTypePerson), ActiveAt: time.Unix(int64(msg.Timestamp), 0).UnixNano()},
	}, store.touches)

	require.NoError(t, projector.SubmitCommitted(context.Background(), channel.Message{
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		MessageSeq:  11,
		ClientMsgNo: "c2",
		Timestamp:   int32(now.Add(time.Second).Unix()),
	}))
	require.Len(t, store.touches, 2)
}

func TestProjectorColdWakeupPagesGroupSubscribers(t *testing.T) {
	now := time.Unix(40*24*60*60, 0)
	store := newProjectorStoreStub()
	key := metadb.ConversationKey{ChannelID: "g1", ChannelType: 2}
	store.cold[key] = metadb.ChannelUpdateLog{
		ChannelID:   "g1",
		ChannelType: 2,
		LastMsgAt:   now.Add(-31 * 24 * time.Hour).UnixNano(),
	}
	store.subscribers[key] = []string{"u1", "u2", "u3"}

	projector := NewProjector(ProjectorOptions{
		Store:              store,
		FlushInterval:      time.Hour,
		DirtyLimit:         8,
		ColdThreshold:      30 * 24 * time.Hour,
		SubscriberPageSize: 2,
		Now:                func() time.Time { return now },
		Async:              func(fn func()) { fn() },
	})

	msg := channel.Message{
		ChannelID:   "g1",
		ChannelType: 2,
		MessageSeq:  10,
		ClientMsgNo: "c1",
		Timestamp:   int32(now.Unix()),
	}
	require.NoError(t, projector.SubmitCommitted(context.Background(), msg))
	require.Equal(t, []subscriberListCall{
		{ChannelID: "g1", ChannelType: 2, AfterUID: "", Limit: 2},
		{ChannelID: "g1", ChannelType: 2, AfterUID: "u2", Limit: 2},
	}, store.subscriberCalls)
	require.ElementsMatch(t, []metadb.UserConversationActivePatch{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: time.Unix(int64(msg.Timestamp), 0).UnixNano()},
		{UID: "u2", ChannelID: "g1", ChannelType: 2, ActiveAt: time.Unix(int64(msg.Timestamp), 0).UnixNano()},
		{UID: "u3", ChannelID: "g1", ChannelType: 2, ActiveAt: time.Unix(int64(msg.Timestamp), 0).UnixNano()},
	}, store.touches)
}

func TestProjectorOverlayReadWinsBeforeColdFlush(t *testing.T) {
	store := newProjectorStoreStub()
	key := metadb.ConversationKey{ChannelID: "g1", ChannelType: 2}
	store.cold[key] = metadb.ChannelUpdateLog{
		ChannelID:       "g1",
		ChannelType:     2,
		UpdatedAt:       time.Unix(90, 0).UnixNano(),
		LastMsgSeq:      9,
		LastClientMsgNo: "cold",
		LastMsgAt:       time.Unix(90, 0).UnixNano(),
	}

	projector := NewProjector(ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
		DirtyLimit:    8,
		ColdThreshold: 30 * 24 * time.Hour,
		Now:           func() time.Time { return time.Unix(100, 0) },
		Async:         func(fn func()) { fn() },
	})

	msg := channel.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 10, ClientMsgNo: "hot", Timestamp: 100}
	require.NoError(t, projector.SubmitCommitted(context.Background(), msg))

	got, err := projector.BatchGetHotChannelUpdates(context.Background(), []metadb.ConversationKey{key})
	require.NoError(t, err)
	require.Equal(t, map[metadb.ConversationKey]metadb.ChannelUpdateLog{
		key: {
			ChannelID:       "g1",
			ChannelType:     2,
			UpdatedAt:       time.Unix(100, 0).UnixNano(),
			LastMsgSeq:      10,
			LastClientMsgNo: "hot",
			LastMsgAt:       time.Unix(100, 0).UnixNano(),
		},
	}, got)
	require.Empty(t, store.updates)
}

func TestProjectorRetriesColdWakeupOnLaterFlush(t *testing.T) {
	now := time.Unix(40*24*60*60, 0)
	store := newProjectorStoreStub()
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	store.cold[metadb.ConversationKey{ChannelID: channelID, ChannelType: int64(frame.ChannelTypePerson)}] = metadb.ChannelUpdateLog{
		ChannelID:   channelID,
		ChannelType: int64(frame.ChannelTypePerson),
		LastMsgAt:   now.Add(-31 * 24 * time.Hour).UnixNano(),
	}
	store.touchErrs = []error{errors.New("transient")}

	projector := NewProjector(ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
		DirtyLimit:    8,
		ColdThreshold: 30 * 24 * time.Hour,
		Now:           func() time.Time { return now },
		Async:         func(fn func()) { fn() },
	})

	msg := channel.Message{
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		MessageSeq:  10,
		ClientMsgNo: "c1",
		Timestamp:   int32(now.Unix()),
	}
	require.NoError(t, projector.SubmitCommitted(context.Background(), msg))
	require.Empty(t, store.touches)
	require.Equal(t, 1, store.touchCalls)

	require.NoError(t, projector.Flush(context.Background()))
	require.ElementsMatch(t, []metadb.UserConversationActivePatch{
		{UID: "u1", ChannelID: channelID, ChannelType: int64(frame.ChannelTypePerson), ActiveAt: time.Unix(int64(msg.Timestamp), 0).UnixNano()},
		{UID: "u2", ChannelID: channelID, ChannelType: int64(frame.ChannelTypePerson), ActiveAt: time.Unix(int64(msg.Timestamp), 0).UnixNano()},
	}, store.touches)
	require.Equal(t, 2, store.touchCalls)
}

func TestProjectorFlushWakeupsSerializesConcurrentRuns(t *testing.T) {
	store := newProjectorStoreStub()
	projector := NewProjector(ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
		DirtyLimit:    8,
		ColdThreshold: 30 * 24 * time.Hour,
		Now:           func() time.Time { return time.Unix(100, 0) },
		Async:         func(fn func()) { fn() },
	}).(*projector)

	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	msg := channel.Message{
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		MessageSeq:  10,
		ClientMsgNo: "c1",
		Timestamp:   100,
	}
	key := metadb.ConversationKey{ChannelID: channelID, ChannelType: int64(frame.ChannelTypePerson)}
	projector.wakeups[key] = msg

	firstTouchStarted := make(chan struct{})
	secondTouchStarted := make(chan struct{})
	releaseFirstTouch := make(chan struct{})
	store.beforeTouch = func(call int) {
		switch call {
		case 1:
			close(firstTouchStarted)
			<-releaseFirstTouch
		case 2:
			close(secondTouchStarted)
		}
	}

	errCh := make(chan error, 2)
	go func() {
		errCh <- projector.flushWakeups(context.Background())
	}()
	<-firstTouchStarted
	go func() {
		errCh <- projector.flushWakeups(context.Background())
	}()
	select {
	case <-secondTouchStarted:
		t.Fatalf("concurrent flush duplicated touch for pending wakeups")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirstTouch)

	require.NoError(t, <-errCh)
	require.NoError(t, <-errCh)
	require.Equal(t, 1, store.touchCalls)
	require.Len(t, store.touches, 2)
}

func TestProjectorFlushWakeupsBatchesPersonTouches(t *testing.T) {
	store := newProjectorStoreStub()
	projector := NewProjector(ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
		DirtyLimit:    8,
		ColdThreshold: 30 * 24 * time.Hour,
		Now:           func() time.Time { return time.Unix(100, 0) },
		Async:         func(fn func()) { fn() },
	}).(*projector)

	channelID1 := runtimechannelid.EncodePersonChannel("u1", "u2")
	channelID2 := runtimechannelid.EncodePersonChannel("u1", "u3")
	projector.wakeups[metadb.ConversationKey{ChannelID: channelID1, ChannelType: int64(frame.ChannelTypePerson)}] = channel.Message{
		ChannelID:   channelID1,
		ChannelType: frame.ChannelTypePerson,
		MessageSeq:  10,
		ClientMsgNo: "c1",
		Timestamp:   100,
	}
	projector.wakeups[metadb.ConversationKey{ChannelID: channelID2, ChannelType: int64(frame.ChannelTypePerson)}] = channel.Message{
		ChannelID:   channelID2,
		ChannelType: frame.ChannelTypePerson,
		MessageSeq:  11,
		ClientMsgNo: "c2",
		Timestamp:   101,
	}

	require.NoError(t, projector.flushWakeups(context.Background()))
	require.Equal(t, 1, store.touchCalls)
	require.ElementsMatch(t, []metadb.UserConversationActivePatch{
		{UID: "u1", ChannelID: channelID1, ChannelType: int64(frame.ChannelTypePerson), ActiveAt: time.Unix(100, 0).UnixNano()},
		{UID: "u2", ChannelID: channelID1, ChannelType: int64(frame.ChannelTypePerson), ActiveAt: time.Unix(100, 0).UnixNano()},
		{UID: "u1", ChannelID: channelID2, ChannelType: int64(frame.ChannelTypePerson), ActiveAt: time.Unix(101, 0).UnixNano()},
		{UID: "u3", ChannelID: channelID2, ChannelType: int64(frame.ChannelTypePerson), ActiveAt: time.Unix(101, 0).UnixNano()},
	}, store.touches)
}

func TestProjectorSubmitCommittedSchedulesSingleWakeupWorker(t *testing.T) {
	now := time.Unix(40*24*60*60, 0)
	store := newProjectorStoreStub()
	channelID1 := runtimechannelid.EncodePersonChannel("u1", "u2")
	channelID2 := runtimechannelid.EncodePersonChannel("u1", "u3")
	store.cold[metadb.ConversationKey{ChannelID: channelID1, ChannelType: int64(frame.ChannelTypePerson)}] = metadb.ChannelUpdateLog{
		ChannelID:   channelID1,
		ChannelType: int64(frame.ChannelTypePerson),
		LastMsgAt:   now.Add(-31 * 24 * time.Hour).UnixNano(),
	}
	store.cold[metadb.ConversationKey{ChannelID: channelID2, ChannelType: int64(frame.ChannelTypePerson)}] = metadb.ChannelUpdateLog{
		ChannelID:   channelID2,
		ChannelType: int64(frame.ChannelTypePerson),
		LastMsgAt:   now.Add(-31 * 24 * time.Hour).UnixNano(),
	}

	var asyncFns []func()
	projector := NewProjector(ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
		DirtyLimit:    8,
		ColdThreshold: 30 * 24 * time.Hour,
		Now:           func() time.Time { return now },
		Async: func(fn func()) {
			asyncFns = append(asyncFns, fn)
		},
	})

	require.NoError(t, projector.SubmitCommitted(context.Background(), channel.Message{
		ChannelID:   channelID1,
		ChannelType: frame.ChannelTypePerson,
		MessageSeq:  10,
		ClientMsgNo: "c1",
		Timestamp:   int32(now.Unix()),
	}))
	require.NoError(t, projector.SubmitCommitted(context.Background(), channel.Message{
		ChannelID:   channelID2,
		ChannelType: frame.ChannelTypePerson,
		MessageSeq:  11,
		ClientMsgNo: "c2",
		Timestamp:   int32(now.Add(time.Second).Unix()),
	}))

	require.Len(t, asyncFns, 1)
	asyncFns[0]()
	require.Equal(t, 1, store.touchCalls)
	require.Len(t, store.touches, 4)
}

func TestProjectorFirstMessageSkipsColdLookup(t *testing.T) {
	store := newProjectorStoreStub()
	channelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	projector := NewProjector(ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
		DirtyLimit:    8,
		ColdThreshold: 30 * 24 * time.Hour,
		Now:           func() time.Time { return time.Unix(100, 0) },
		Async:         func(fn func()) { fn() },
	})

	require.NoError(t, projector.SubmitCommitted(context.Background(), channel.Message{
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		MessageSeq:  1,
		ClientMsgNo: "c1",
		Timestamp:   100,
	}))
	require.Zero(t, store.batchGetCalls)
}

type projectorStoreStub struct {
	mu              sync.Mutex
	cold            map[metadb.ConversationKey]metadb.ChannelUpdateLog
	updates         []metadb.ChannelUpdateLog
	touches         []metadb.UserConversationActivePatch
	touchCalls      int
	batchGetCalls   int
	touchErrs       []error
	beforeTouch     func(call int)
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
	return &projectorStoreStub{
		cold:        make(map[metadb.ConversationKey]metadb.ChannelUpdateLog),
		subscribers: make(map[metadb.ConversationKey][]string),
	}
}

func (s *projectorStoreStub) BatchGetChannelUpdateLogs(_ context.Context, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelUpdateLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.batchGetCalls++
	out := make(map[metadb.ConversationKey]metadb.ChannelUpdateLog, len(keys))
	for _, key := range keys {
		if entry, ok := s.cold[key]; ok {
			out[key] = entry
		}
	}
	return out, nil
}

func (s *projectorStoreStub) UpsertChannelUpdateLogs(_ context.Context, entries []metadb.ChannelUpdateLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, entry := range entries {
		key := metadb.ConversationKey{ChannelID: entry.ChannelID, ChannelType: entry.ChannelType}
		s.cold[key] = entry
		s.updates = append(s.updates, entry)
	}
	sort.Slice(s.updates, func(i, j int) bool {
		if s.updates[i].ChannelType != s.updates[j].ChannelType {
			return s.updates[i].ChannelType < s.updates[j].ChannelType
		}
		return s.updates[i].ChannelID < s.updates[j].ChannelID
	})
	return nil
}

func (s *projectorStoreStub) TouchUserConversationActiveAt(_ context.Context, patches []metadb.UserConversationActivePatch) error {
	s.mu.Lock()
	s.touchCalls++
	call := s.touchCalls
	hook := s.beforeTouch
	var err error
	if len(s.touchErrs) > 0 {
		err = s.touchErrs[0]
		s.touchErrs = s.touchErrs[1:]
	}
	s.mu.Unlock()

	if hook != nil {
		hook(call)
	}
	if err != nil {
		if err != nil {
			return err
		}
	}

	s.mu.Lock()
	s.touches = append(s.touches, patches...)
	s.mu.Unlock()
	return nil
}

func (s *projectorStoreStub) ListChannelSubscribers(_ context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	s.mu.Lock()
	s.subscriberCalls = append(s.subscriberCalls, subscriberListCall{
		ChannelID:   channelID,
		ChannelType: channelType,
		AfterUID:    afterUID,
		Limit:       limit,
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
