package message

import (
	"context"
	"errors"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"strconv"
	"strings"
	"testing"
)

type updateStoreStub struct {
	head      metadb.MessageUpdateHead
	rows      []metadb.MessageUpdate
	mutations []metadb.MessageUpdateMutation
	reads     []metadb.MessageUpdateRead
	runtime   metadb.ChannelRuntimeMeta
}

type commitResultStore struct {
	updateStoreStub
	status string
	err    error
}

func (s *commitResultStore) ApplyMessageUpdate(_ context.Context, q metadb.MessageUpdateMutation) (metadb.MessageUpdateMutationResult, error) {
	s.mutations = append(s.mutations, q)
	return metadb.MessageUpdateMutationResult{Status: s.status, Head: s.head, Request: metadb.MessageUpdateRequest{
		MessageID: q.MessageID, MessageSeq: q.MessageSeq, Version: 7}}, s.err
}

func TestMessageUpdateSchedulesOnlyDurableSuccess(t *testing.T) {
	for _, status := range []string{"ok", "version_conflict", "storage_error"} {
		t.Run(status, func(t *testing.T) {
			store := &commitResultStore{updateStoreStub: updateStoreStub{head: metadb.MessageUpdateHead{Generation: "g", ReplicaSet: "1"}}, status: status}
			if status == "storage_error" {
				store.err = errors.New("unconfirmed commit")
			}
			var scheduled []metadb.MessageUpdate
			a := New(Options{Updates: store, UpdateCommitted: func(task metadb.MessageUpdate) {
				if len(store.mutations) != 1 {
					t.Fatal("scheduled before durable mutation")
				}
				scheduled = append(scheduled, task)
			}, LookupReader: scanFunction(func(context.Context, []MessageScanQuery) ([]MessageScanResult, error) {
				return []MessageScanResult{{Messages: []SyncedMessage{{ChannelID: "g", ChannelType: 2, MessageID: 10, MessageSeq: 20}}}}, nil
			})})
			_, err := a.UpdateMessage(context.Background(), UpdateMessageCommand{ChannelID: "g", ChannelType: 2, MessageID: 10, RequestID: "retry", Payload: []byte("new")})
			if status != "ok" {
				if err == nil || len(scheduled) != 0 {
					t.Fatal("failed commit scheduled a hint")
				}
				return
			}
			if err != nil || len(scheduled) != 1 {
				t.Fatalf("scheduled=%v err=%v", scheduled, err)
			}
			task := scheduled[0]
			if task.ChannelID != "g" || task.ChannelType != 2 || task.MessageID != 10 || task.MessageSeq != 20 || task.Version != 7 || len(task.Payload) != 0 {
				t.Fatalf("must schedule returned idempotent version, without payload: %+v", task)
			}
		})
	}
}

func (s *updateStoreStub) ApplyMessageUpdate(_ context.Context, q metadb.MessageUpdateMutation) (metadb.MessageUpdateMutationResult, error) {
	s.mutations = append(s.mutations, q)
	return metadb.MessageUpdateMutationResult{Status: "ok", Head: s.head, Request: metadb.MessageUpdateRequest{MessageID: q.MessageID, MessageSeq: q.MessageSeq, Version: q.ExpectedVersion + 1}}, nil
}
func (s *updateStoreStub) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return s.runtime, nil
}
func (s *updateStoreStub) ReadMessageUpdatesBatch(_ context.Context, qs []metadb.MessageUpdateRead) ([]metadb.MessageUpdatePage, error) {
	out := make([]metadb.MessageUpdatePage, len(qs))
	for i, q := range qs {
		s.reads = append(s.reads, q)
		out[i] = metadb.MessageUpdatePage{Head: s.head, Next: s.head.UpdateSeq, Through: s.head.UpdateSeq}
		if q.Through > 0 {
			out[i].Through = q.Through
			out[i].Next = q.Through
		}
		if q.Limit > 0 || len(q.IDs) > 0 {
			for _, row := range s.rows {
				if q.Limit > 0 && (row.UpdateSeq <= q.After || row.UpdateSeq > out[i].Through) {
					continue
				}
				if q.Limit > 0 && len(out[i].Updates) == q.Limit {
					out[i].More = true
					out[i].Next = out[i].Updates[len(out[i].Updates)-1].UpdateSeq
					break
				}
				out[i].Updates = append(out[i].Updates, row)
			}
		}
	}
	return out, nil
}

func TestMessageUpdatesBaselineVisibilityAndRestore(t *testing.T) {
	ctx := context.Background()
	store := &updateStoreStub{head: metadb.MessageUpdateHead{Generation: "g1", UpdateSeq: 5}}
	member := &recordingSyncMembershipStore{ok: true, row: metadb.UserChannelMembership{SourceVersion: 1, JoinSeq: 2, DeletedToSeq: 7}}
	epoch := uint64(0)
	baseReads := 0
	a := New(Options{Updates: store, ContentEpoch: func(context.Context) (uint64, error) { return epoch, nil }, Memberships: member, Reader: &recordingChannelMessageReader{}, LookupReader: scanFunction(func(_ context.Context, qs []MessageScanQuery) ([]MessageScanResult, error) {
		baseReads += len(qs)
		out := make([]MessageScanResult, len(qs))
		for i, q := range qs {
			if q.MinSeq != 8 {
				t.Fatalf("visibility floor=%d", q.MinSeq)
			}
			if q.MessageID == 10 {
				out[i].Messages = []SyncedMessage{{MessageID: 10, MessageSeq: 10, ChannelID: "g", ChannelType: 2, Payload: []byte("newer"), Version: 3}}
			}
		}
		return out, nil
	})})
	q := MessageUpdatesQuery{LoginUID: "u", ChannelID: "g", ChannelType: 2}
	initial, err := a.MessageUpdates(ctx, q)
	if err != nil || !initial.ResetRequired || baseReads != 0 {
		t.Fatalf("baseline=%+v reads=%d err=%v", initial, baseReads, err)
	}
	cursor, _ := decodeUpdateCursor(initial.NextUpdateCursor)
	if cursor.After != 5 {
		t.Fatal(cursor)
	}
	q.UpdateCursor = initial.NextUpdateCursor
	store.head.UpdateSeq = 7
	store.rows = []metadb.MessageUpdate{{MessageID: 9, MessageSeq: 9, Version: 1, UpdateSeq: 6}, {MessageID: 10, MessageSeq: 10, Version: 2, UpdateSeq: 7, Payload: []byte("older")}}
	page, err := a.MessageUpdates(ctx, q)
	if err != nil || len(page.Updates) != 1 || string(page.Updates[0].Payload) != "newer" || page.Updates[0].Version != 3 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	next, _ := decodeUpdateCursor(page.NextUpdateCursor)
	if next.After != 7 {
		t.Fatal(next)
	}
	epoch = 1
	reset, err := a.MessageUpdates(ctx, q)
	if err != nil || !reset.ResetRequired || len(reset.Updates) != 0 {
		t.Fatalf("restore=%+v err=%v", reset, err)
	}
	epoch = 0
	member.row.SourceVersion++
	reset, err = a.MessageUpdates(ctx, q)
	if err != nil || !reset.ResetRequired {
		t.Fatalf("rejoin=%+v err=%v", reset, err)
	}
	member.row.SourceVersion--
	store.head.Generation = "recreated"
	reset, err = a.MessageUpdates(ctx, q)
	if err != nil || !reset.ResetRequired {
		t.Fatalf("recreate=%+v err=%v", reset, err)
	}
	q.LoginUID = "another"
	if _, err = a.MessageUpdates(ctx, q); !errors.Is(err, ErrUpdateInvalid) {
		t.Fatalf("cross-user cursor=%v", err)
	}
}

func TestMessageUpdatesLongIdentitiesKeepUsableBoundedCursors(t *testing.T) {
	// Exercise the supported storage key limit with JSON-escaped identities;
	// raw identity cursors exceed their input bound even far below this limit.
	uid := strings.Repeat(`"\`, 32767) + "u"
	channelID := strings.Repeat("<", 65535)
	store := &updateStoreStub{head: metadb.MessageUpdateHead{Generation: "g1", UpdateSeq: 5}}
	a := New(Options{Updates: store, Memberships: &recordingSyncMembershipStore{ok: true}, Reader: &recordingChannelMessageReader{}, LookupReader: scanFunction(func(_ context.Context, qs []MessageScanQuery) ([]MessageScanResult, error) {
		out := make([]MessageScanResult, len(qs))
		for i, q := range qs {
			out[i].Messages = []SyncedMessage{{MessageID: q.MessageID, MessageSeq: q.MessageID, ChannelID: channelID, ChannelType: 2}}
		}
		return out, nil
	})})
	q := MessageUpdatesQuery{LoginUID: uid, ChannelID: channelID, ChannelType: 2, Limit: 1}
	baseline, err := a.MessageUpdates(context.Background(), q)
	if err != nil || !baseline.ResetRequired || len(baseline.Updates) != 0 {
		t.Fatalf("baseline reset=%v rows=%d error=%v", baseline.ResetRequired, len(baseline.Updates), err)
	}
	assertBounded := func(token string) {
		t.Helper()
		cursor, err := decodeUpdateCursor(token)
		if err != nil || len(token) > 512 || cursor.Format != 2 || cursor.Binding == "" || cursor.UID != "" || cursor.ChannelID != "" {
			t.Fatalf("cursor length=%d format=%d error=%v", len(token), cursor.Format, err)
		}
	}
	assertBounded(baseline.NextUpdateCursor)
	store.head.UpdateSeq = 7
	store.rows = []metadb.MessageUpdate{{MessageID: 6, MessageSeq: 6, Version: 1, UpdateSeq: 6, Payload: []byte("six")}, {MessageID: 7, MessageSeq: 7, Version: 1, UpdateSeq: 7, Payload: []byte("seven")}}
	q.UpdateCursor = baseline.NextUpdateCursor
	first, err := a.MessageUpdates(context.Background(), q)
	if err != nil || first.ResetRequired || !first.More || len(first.Updates) != 1 || first.Updates[0].MessageID != 6 {
		t.Fatalf("first page reset=%v more=%v rows=%d error=%v", first.ResetRequired, first.More, len(first.Updates), err)
	}
	assertBounded(first.NextUpdateCursor)
	q.UpdateCursor = first.NextUpdateCursor
	last, err := a.MessageUpdates(context.Background(), q)
	if err != nil || last.ResetRequired || last.More || len(last.Updates) != 1 || last.Updates[0].MessageID != 7 {
		t.Fatalf("last page reset=%v more=%v rows=%d error=%v", last.ResetRequired, last.More, len(last.Updates), err)
	}
	assertBounded(last.NextUpdateCursor)
	q.UpdateCursor = last.NextUpdateCursor
	caughtUp, err := a.MessageUpdates(context.Background(), q)
	if err != nil || caughtUp.ResetRequired || caughtUp.More || len(caughtUp.Updates) != 0 {
		t.Fatalf("caught-up reset=%v more=%v rows=%d error=%v", caughtUp.ResetRequired, caughtUp.More, len(caughtUp.Updates), err)
	}
	for _, other := range []MessageUpdatesQuery{
		{LoginUID: "other", ChannelID: channelID, ChannelType: 2, UpdateCursor: last.NextUpdateCursor},
		{LoginUID: uid, ChannelID: "other", ChannelType: 2, UpdateCursor: last.NextUpdateCursor},
		{LoginUID: uid, ChannelID: channelID, ChannelType: 3, UpdateCursor: last.NextUpdateCursor},
	} {
		if _, err := a.MessageUpdates(context.Background(), other); !errors.Is(err, ErrUpdateInvalid) {
			t.Fatalf("foreign cursor accepted: %v", err)
		}
	}
}

func TestMessageUpdatesCursorFormatMigrationAndValidation(t *testing.T) {
	store := &updateStoreStub{head: metadb.MessageUpdateHead{Generation: "g", UpdateSeq: 9}}
	a := New(Options{Updates: store, Memberships: &recordingSyncMembershipStore{ok: true}, Reader: &recordingChannelMessageReader{}, LookupReader: scanFunction(func(context.Context, []MessageScanQuery) ([]MessageScanResult, error) {
		t.Fatal("format migration must bootstrap instead of reading old progress")
		return nil, nil
	})})
	q := MessageUpdatesQuery{LoginUID: "u", ChannelID: "g", ChannelType: 2}
	old := updateCursor{Format: 1, UID: "u", ChannelID: "g", ChannelType: 2, Generation: "g", After: 4, Through: 7}
	q.UpdateCursor = encodeUpdateCursor(old)
	reset, err := a.MessageUpdates(context.Background(), q)
	if err != nil || !reset.ResetRequired || len(reset.Updates) != 0 {
		t.Fatalf("legacy migration=%+v error=%v", reset, err)
	}
	next, err := decodeUpdateCursor(reset.NextUpdateCursor)
	if err != nil || next.Format != 2 || next.After != 9 || next.Through != 0 {
		t.Fatalf("migration baseline=%+v error=%v", next, err)
	}
	for _, cursor := range []updateCursor{
		{Format: 1, UID: "other", ChannelID: "g", ChannelType: 2},
		{Format: 1, UID: "u", ChannelID: "other", ChannelType: 2},
		{Format: 1, UID: "u", ChannelID: "g", ChannelType: 3},
		{Format: 1, UID: "u", ChannelID: "g", ChannelType: 2, Binding: next.Binding},
		{Format: 2},
		{Format: 2, Binding: strings.Repeat("z", 64)},
		{Format: 2, Binding: next.Binding, UID: "u"},
		{Format: 3, Binding: next.Binding},
	} {
		q.UpdateCursor = encodeUpdateCursor(cursor)
		if _, err := a.MessageUpdates(context.Background(), q); !errors.Is(err, ErrUpdateInvalid) {
			t.Fatalf("invalid/foreign format-%d cursor accepted: %v", cursor.Format, err)
		}
	}
}

func TestMessageUpdateRejectsCMDAndStream(t *testing.T) {
	store := &updateStoreStub{head: metadb.MessageUpdateHead{Generation: "g"}}
	original := SyncedMessage{MessageID: 1, MessageSeq: 1, ChannelID: "g", ChannelType: 2, Payload: []byte("old")}
	a := New(Options{Updates: store, CommandChannelSuffix: "$commands", LookupReader: scanFunction(func(context.Context, []MessageScanQuery) ([]MessageScanResult, error) {
		return []MessageScanResult{{Messages: []SyncedMessage{original}}}, nil
	})})
	q := UpdateMessageCommand{ChannelID: "g$commands", ChannelType: 2, MessageID: 1, RequestID: "r", Payload: []byte("new")}
	if _, err := a.UpdateMessage(context.Background(), q); !errors.Is(err, ErrNotUpdatable) {
		t.Fatal(err)
	}
	if len(store.reads) != 0 || len(store.mutations) != 0 {
		t.Fatal("CMD reached edit storage")
	}
	q.ChannelID = "g"
	original.Flags.SyncOnce = true
	if _, err := a.UpdateMessage(context.Background(), q); !errors.Is(err, ErrNotUpdatable) {
		t.Fatal(err)
	}
	original.Flags.SyncOnce = false
	original.Setting = 1 << 1
	// Stream setting is checked through the same legacy helper as history reads.
	for setting := 0; setting < 256; setting++ {
		if isLegacyStreamMessage(uint8(setting)) {
			original.Setting = uint8(setting)
			break
		}
	}
	if _, err := a.UpdateMessage(context.Background(), q); !errors.Is(err, ErrNotUpdatable) {
		t.Fatal(err)
	}
	if len(store.mutations) != 0 {
		t.Fatal("ineligible message was mutated")
	}
}

type updateHintStub struct {
	uids []string
	hint MessageUpdateHint
	err  error
}

func (h *updateHintStub) SendMessageUpdateHint(_ context.Context, uids []string, hint MessageUpdateHint) error {
	h.uids = uids
	h.hint = hint
	return h.err
}

type updateSubscribersStub struct {
	after string
	limit int
}

func (s *updateSubscribersStub) ListChannelSubscribersAuthoritative(_ context.Context, _ string, _ int64, after string, limit int) ([]string, string, bool, error) {
	s.after = after
	s.limit = limit
	return []string{"u2"}, "u2", false, nil
}
func TestMessageUpdateNotificationProgressAndRetry(t *testing.T) {
	store := &updateStoreStub{head: metadb.MessageUpdateHead{Generation: "g"}, rows: []metadb.MessageUpdate{{ChannelID: "g", ChannelType: 2, MessageID: 1, MessageSeq: 10, Version: 2, Pending: 1, PendingAfterUID: "u1"}}}
	hints := &updateHintStub{err: errors.New("route unavailable")}
	subscribers := &updateSubscribersStub{}
	a := New(Options{Updates: store, UpdateHints: hints, UpdateSubscribers: subscribers, LookupReader: scanFunction(func(context.Context, []MessageScanQuery) ([]MessageScanResult, error) {
		return []MessageScanResult{{Messages: []SyncedMessage{{MessageID: 1, MessageSeq: 10}}}}, nil
	})})
	task := store.rows[0]
	if _, err := a.DispatchMessageUpdate(context.Background(), task); err == nil {
		t.Fatal("missing transient delivery error")
	}
	if len(store.mutations) != 0 {
		t.Fatal("failed delivery advanced durable cursor")
	}
	hints.err = nil
	if _, err := a.DispatchMessageUpdate(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if subscribers.after != "u1" || subscribers.limit != 128 || len(store.mutations) != 1 {
		t.Fatalf("subscriber=%+v mutations=%+v", subscribers, store.mutations)
	}
	mutation := store.mutations[0]
	if mutation.Op != "progress" || mutation.AfterUID != "u2" || mutation.ExpectedVersion != 2 || len(mutation.Payload) != 0 {
		t.Fatal(mutation)
	}
	task.Version = 1
	if _, err := a.DispatchMessageUpdate(context.Background(), task); err != nil || len(store.mutations) != 1 {
		t.Fatalf("stale task wrote progress: %v", err)
	}
}

func TestMessageUpdatesGrowthBudget(t *testing.T) {
	store := &updateStoreStub{head: metadb.MessageUpdateHead{Generation: "g", UpdateSeq: 10}}
	for i := uint64(1); i <= 10; i++ {
		store.rows = append(store.rows, metadb.MessageUpdate{MessageID: i, MessageSeq: i, Version: 1, UpdateSeq: i, Payload: []byte("small")})
	}
	a := New(Options{Updates: store, Memberships: &recordingSyncMembershipStore{ok: true, row: metadb.UserChannelMembership{SourceVersion: 1}}, Reader: &recordingChannelMessageReader{}, LookupReader: scanFunction(func(_ context.Context, qs []MessageScanQuery) ([]MessageScanResult, error) {
		out := make([]MessageScanResult, len(qs))
		for i, q := range qs {
			out[i].Messages = []SyncedMessage{{MessageID: q.MessageID, MessageSeq: q.MessageID, ChannelID: "g", ChannelType: 2, Version: 2, Payload: make([]byte, metadb.MaxMessageUpdatePayload)}}
		}
		return out, nil
	})})
	cursor := encodeUpdateCursor(updateCursor{Format: 2, Binding: messageUpdateCursorBinding("u", ChannelID{ID: "g", Type: 2}), Generation: "g", SourceVersion: 1})
	got, err := a.MessageUpdates(context.Background(), MessageUpdatesQuery{LoginUID: "u", ChannelID: "g", ChannelType: 2, UpdateCursor: cursor, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, m := range got.Updates {
		total += len(m.Payload)
	}
	if total > metadb.MaxMessageUpdatePageBytes {
		t.Fatalf("unbounded assembled delta: %d bytes, budget %d", total, metadb.MaxMessageUpdatePageBytes)
	}
	if !got.More || len(got.Updates) != 7 {
		t.Fatalf("first page rows=%d more=%v", len(got.Updates), got.More)
	}
	next, err := a.MessageUpdates(context.Background(), MessageUpdatesQuery{LoginUID: "u", ChannelID: "g", ChannelType: 2, UpdateCursor: got.NextUpdateCursor, Limit: 100})
	if err != nil || next.More || len(next.Updates) != 3 || next.Updates[0].MessageID != 8 || next.Updates[2].MessageID != 10 {
		t.Fatalf("continuation=%+v err=%v", next, err)
	}

}

type largeGroupUpdateStore struct{ updateStoreStub }

func (s *largeGroupUpdateStore) ApplyMessageUpdate(ctx context.Context, q metadb.MessageUpdateMutation) (metadb.MessageUpdateMutationResult, error) {
	if q.Op == "progress" {
		s.rows[0].PendingAfterUID = q.AfterUID
	}
	if q.Op == "ack" {
		s.rows[0].Pending = 0
	}
	return s.updateStoreStub.ApplyMessageUpdate(ctx, q)
}

type largeGroupSubscribers struct{ count int }

func (s largeGroupSubscribers) ListChannelSubscribersAuthoritative(_ context.Context, _ string, _ int64, after string, limit int) ([]string, string, bool, error) {
	start, _ := strconv.Atoi(after)
	end := min(start+limit, s.count)
	uids := make([]string, end-start)
	for i := range uids {
		uids[i] = strconv.Itoa(start + i + 1)
	}
	return uids, strconv.Itoa(end), end == s.count, nil
}

type countedUpdateHints struct{ recipients, pages int }

func (h *countedUpdateHints) SendMessageUpdateHint(_ context.Context, uids []string, _ MessageUpdateHint) error {
	h.recipients += len(uids)
	h.pages++
	return nil
}
func TestMessageUpdateHundredThousandRecipientsWithoutPayloadReads(t *testing.T) {
	task := metadb.MessageUpdate{ChannelID: "g", ChannelType: 2, MessageID: 1, MessageSeq: 1, Version: 1, Pending: 1}
	store := &largeGroupUpdateStore{updateStoreStub{head: metadb.MessageUpdateHead{Generation: "g"}, rows: []metadb.MessageUpdate{task}}}
	hints := &countedUpdateHints{}
	a := New(Options{Updates: store, UpdateSubscribers: largeGroupSubscribers{count: 100000}, UpdateHints: hints, LookupReader: scanFunction(func(context.Context, []MessageScanQuery) ([]MessageScanResult, error) {
		t.Fatal("notification fanout reread message payload")
		return nil, nil
	})})
	for pages := 0; pages < 1000; pages++ {
		more, err := a.DispatchMessageUpdate(context.Background(), task)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	if hints.recipients != 100000 || hints.pages != 782 || store.rows[0].Pending != 0 {
		t.Fatalf("recipients=%d pages=%d pending=%d", hints.recipients, hints.pages, store.rows[0].Pending)
	}
	for _, read := range store.reads {
		if !read.IncludePending {
			t.Fatal("notification read full latest content")
		}
	}
}
