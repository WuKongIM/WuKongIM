package channelwrite

import (
	"context"
	"reflect"
	"sync"
	"testing"
)

func TestCursorStoreAndCommittedReaderAreIgnoredForBestEffortPostCommit(t *testing.T) {
	router := &scriptedRecipientRouterForCommitTest{}
	store := &recordingCursorStoreForTest{loaded: 3}
	appender := newRecordingAppenderForAppendTest()
	appender.nextSeq = 3005
	reader := &recordingCommittedReaderForTest{
		pages: [][]CommittedMessage{{
			{
				MessageID:         3004,
				MessageSeq:        4,
				ChannelID:         "room",
				ChannelType:       2,
				FromUID:           "u9",
				Payload:           []byte("replay"),
				MessageScopedUIDs: []string{"u2"},
			},
		}},
	}
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(3005),
		Appender:                   appender,
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientRouter:            router,
		RecipientBatchSize:         16,
		CursorStore:                store,
		CommittedReader:            reader,
		ReplayPageSize:             1,
	})
	target := localTargetForAppendTest("room")
	item := appendSendItemForTest("u1", "room", "live")
	item.Command.MessageScopedUIDs = []string{"u3"}

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{item})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 3005, 3005)

	router.waitCalls(t, 1)
	if got := router.messageIDs(); !reflect.DeepEqual(got, []uint64{3005}) {
		t.Fatalf("recipient dispatch message ids = %#v, want only live in-memory event", got)
	}
	if len(reader.calls) != 0 {
		t.Fatalf("reader calls = %d, want committed reader ignored", len(reader.calls))
	}
	if got := store.storedSeqs(); len(got) != 0 {
		t.Fatalf("stored cursor seqs = %#v, want no post-commit checkpoint", got)
	}
}

func TestCursorStoreErrorsDoNotRetryOrBlockBestEffortPostCommit(t *testing.T) {
	router := &scriptedRecipientRouterForCommitTest{}
	store := &recordingCursorStoreForTest{storeErrs: []error{assertErrForCursorTest}}
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(3100),
		Appender:                   newRecordingAppenderForAppendTest(),
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientRouter:            router,
		RecipientBatchSize:         16,
		CursorStore:                store,
		ReplayPageSize:             2,
	})
	target := localTargetForAppendTest("room")
	item := appendSendItemForTest("u1", "room", "payload")
	item.Command.MessageScopedUIDs = []string{"u2"}

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{item})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 3100, 1)

	router.waitCalls(t, 1)
	if got := router.messageIDs(); !reflect.DeepEqual(got, []uint64{3100}) {
		t.Fatalf("recipient dispatch message ids = %#v, want no retry", got)
	}
	if got := store.storedSeqs(); len(got) != 0 {
		t.Fatalf("stored cursor seqs = %#v, want no checkpoint writes", got)
	}
	waitCommitBacklogForTest(t, group, target.ChannelID, 0)
}

func TestNilCursorStoreReplaysOnlyInMemoryCommittedEvents(t *testing.T) {
	router := &scriptedRecipientRouterForCommitTest{}
	reader := &recordingCommittedReaderForTest{
		pages: [][]CommittedMessage{{
			{MessageID: 3200, MessageSeq: 1, ChannelID: "room", ChannelType: 2, MessageScopedUIDs: []string{"u2"}},
		}},
	}
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                1,
		MessageID:                  newSequenceIDsForPrepare(3201),
		Appender:                   newRecordingAppenderForAppendTest(),
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientRouter:            router,
		RecipientBatchSize:         16,
		CommittedReader:            reader,
		ReplayPageSize:             1,
	})
	target := localTargetForAppendTest("room")
	item := appendSendItemForTest("u1", "room", "live")
	item.Command.MessageScopedUIDs = []string{"u3"}

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{item})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 3201, 1)

	router.waitCalls(t, 1)
	if got := router.messageIDs(); !reflect.DeepEqual(got, []uint64{3201}) {
		t.Fatalf("recipient dispatch message ids = %#v, want only live in-memory event", got)
	}
	if len(reader.calls) != 0 {
		t.Fatalf("reader calls = %d, want nil cursor store to disable durable replay", len(reader.calls))
	}
}

var assertErrForCursorTest = &cursorTestError{}

type cursorTestError struct{}

func (*cursorTestError) Error() string { return "cursor should be ignored" }

type recordingCursorStoreForTest struct {
	mu        sync.Mutex
	loaded    uint64
	loadErr   error
	loadErrs  []error
	storeErrs []error
	stored    []uint64
}

func (s *recordingCursorStoreForTest) LoadPostCommitCursor(context.Context, ChannelID) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return 0, s.loadErr
	}
	if len(s.loadErrs) > 0 {
		err := s.loadErrs[0]
		s.loadErrs = s.loadErrs[1:]
		if err != nil {
			return 0, err
		}
	}
	return s.loaded, nil
}

func (s *recordingCursorStoreForTest) StorePostCommitCursor(_ context.Context, _ ChannelID, seq uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.storeErrs) > 0 {
		err := s.storeErrs[0]
		s.storeErrs = s.storeErrs[1:]
		if err != nil {
			return err
		}
	}
	if seq > s.loaded {
		s.loaded = seq
		s.stored = append(s.stored, seq)
	}
	return nil
}

func (s *recordingCursorStoreForTest) storedSeqs() []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uint64(nil), s.stored...)
}

type recordingCommittedReaderForTest struct {
	mu    sync.Mutex
	pages [][]CommittedMessage
	calls []committedReaderCallForTest
}

type committedReaderCallForTest struct {
	channel ChannelID
	fromSeq uint64
	limit   int
}

func (r *recordingCommittedReaderForTest) ReadCommittedFrom(_ context.Context, channel ChannelID, fromSeq uint64, limit int) ([]CommittedMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, committedReaderCallForTest{channel: channel, fromSeq: fromSeq, limit: limit})
	if len(r.pages) == 0 {
		return nil, nil
	}
	page := r.pages[0]
	r.pages = r.pages[1:]
	out := make([]CommittedMessage, len(page))
	for i := range page {
		out[i] = page[i].Clone()
	}
	return out, nil
}
