package conversation

import (
	"context"
	"reflect"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestListBuildsConversationsFromMembershipPage(t *testing.T) {
	directory := &membershipDirectoryStore{
		rows: []metadb.UserChannelMembership{
			{UID: "u1", ChannelID: "gone", ChannelType: 2, Tombstone: true, ActivatedAt: 500},
			{UID: "u1", ChannelID: "visible", ChannelType: 2, JoinSeq: 6, ReadSeq: 8, DeletedToSeq: 5, ActivatedAt: 400},
			{UID: "u1", ChannelID: "active-empty", ChannelType: 2, JoinSeq: 20, ReadSeq: 19, DeletedToSeq: 19, ActivatedAt: 300},
			{UID: "u1", ChannelID: "inactive-empty", ChannelType: 2, JoinSeq: 30, ReadSeq: 29, DeletedToSeq: 29},
		},
		cursor: metadb.UserChannelMembershipCursor{ActivatedAt: 0, ChannelID: "inactive-empty", ChannelType: 2},
		done:   false,
	}
	hydrator := &membershipHeadHydrator{results: []HydrationResult{
		{Key: ConversationKey{ChannelID: "visible", ChannelType: 2}, Outcome: HydrationOK, ReadThroughSeq: 12, RetentionThroughSeq: 7, CurrentUserLastSendSeq: 10, LastMessage: &LastMessage{MessageID: 12, MessageSeq: 12, Payload: []byte("last")}},
		{Key: ConversationKey{ChannelID: "active-empty", ChannelType: 2}, Outcome: HydrationNoVisibleMessage, ReadThroughSeq: 19},
		{Key: ConversationKey{ChannelID: "inactive-empty", ChannelType: 2}, Outcome: HydrationNoVisibleMessage, ReadThroughSeq: 29},
	}}
	app := New(Options{Directory: directory, Hydrator: hydrator})

	result, err := app.List(context.Background(), ListRequest{UID: "u1", Limit: 5})
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if result.Done || !result.HasMore || result.NextCursor.ChannelID != "inactive-empty" {
		t.Fatalf("page state = done=%v hasMore=%v cursor=%+v", result.Done, result.HasMore, result.NextCursor)
	}
	if got, want := result.Deletes, []ConversationKey{{ChannelID: "gone", ChannelType: 2}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deletes = %#v, want %#v", got, want)
	}
	if len(result.Items) != 2 || result.Items[0].ChannelID != "visible" || result.Items[0].Unread != 2 || result.Items[0].LastMessage == nil {
		t.Fatalf("visible conversation = %#v", result.Items)
	}
	if result.Items[1].ChannelID != "active-empty" || result.Items[1].Unread != 0 || result.Items[1].LastMessage != nil {
		t.Fatalf("active empty conversation = %#v", result.Items[1])
	}
	if len(hydrator.memberships) != 3 {
		t.Fatalf("hydrated memberships = %#v, want tombstone bypassed", hydrator.memberships)
	}
	result.Items[0].LastMessage.Payload[0] = 'X'
	if string(hydrator.results[0].LastMessage.Payload) != "last" {
		t.Fatal("List() returned aliased message payload")
	}
}

func TestListAllowsEmptyNonterminalMembershipPage(t *testing.T) {
	app := New(Options{
		Directory: &membershipDirectoryStore{
			rows:   []metadb.UserChannelMembership{{UID: "u1", ChannelID: "inactive", ChannelType: 2, JoinSeq: 2}},
			cursor: metadb.UserChannelMembershipCursor{ChannelID: "inactive", ChannelType: 2},
			done:   false,
		},
		Hydrator: &membershipHeadHydrator{results: []HydrationResult{{
			Key: ConversationKey{ChannelID: "inactive", ChannelType: 2}, Outcome: HydrationNoVisibleMessage,
		}}},
	})
	result, err := app.List(context.Background(), ListRequest{UID: "u1", Limit: 1})
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(result.Items) != 0 || result.Done || !result.HasMore || result.NextCursor.ChannelID != "inactive" {
		t.Fatalf("empty nonterminal page = %+v", result)
	}
}

func TestListReturnsCoverageAndRequiresResetWhenTombstonesExpired(t *testing.T) {
	app := New(Options{
		Directory:               &membershipDirectoryStore{done: true},
		Hydrator:                &membershipHeadHydrator{},
		Now:                     func() time.Time { return time.Unix(0, 100) },
		TombstonesRetainedSince: func() int64 { return 50 },
	})
	result, err := app.List(context.Background(), ListRequest{UID: "u1", CompletedCoverage: 40})
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if result.Coverage != 100 || result.TombstonesRetainedSince != 50 || !result.ResetRequired {
		t.Fatalf("coverage result = %+v", result)
	}
}

type membershipDirectoryStore struct {
	rows   []metadb.UserChannelMembership
	cursor metadb.UserChannelMembershipCursor
	done   bool
}

func (s *membershipDirectoryStore) ListUserChannelMembershipPage(_ context.Context, _ string, _ metadb.UserChannelMembershipCursor, limit int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error) {
	rows := s.rows
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return append([]metadb.UserChannelMembership(nil), rows...), s.cursor, s.done, nil
}

type membershipHeadHydrator struct {
	results     []HydrationResult
	memberships []metadb.UserChannelMembership
}

func (s *membershipHeadHydrator) HydrateConversationHeads(_ context.Context, _ string, memberships []metadb.UserChannelMembership, keepUnread ...uint64) ([]HydrationResult, error) {
	s.memberships = append([]metadb.UserChannelMembership(nil), memberships...)
	return append([]HydrationResult(nil), s.results...), nil
}

func (h *membershipHeadHydrator) HydratePersistedConversationHeads(ctx context.Context, uid string, rows []metadb.UserChannelMembership) ([]HydrationResult, error) {
	return h.HydrateConversationHeads(ctx, uid, rows)
}

func TestListFailsWholePageAndRetriesOriginalCursor(t *testing.T) {
	directory := &membershipDirectoryStore{rows: []metadb.UserChannelMembership{{UID: "u", ChannelID: "ok", ChannelType: 2, JoinSeq: 1}, {UID: "u", ChannelID: "bad", ChannelType: 2, JoinSeq: 1}}, done: true}
	hydrator := &membershipHeadHydrator{results: []HydrationResult{{Key: ConversationKey{ChannelID: "ok", ChannelType: 2}, Outcome: HydrationOK, ReadThroughSeq: 1, LastMessage: &LastMessage{MessageSeq: 1}}, {Key: ConversationKey{ChannelID: "bad", ChannelType: 2}, Outcome: HydrationRetryable}}}
	app := New(Options{Directory: directory, Hydrator: hydrator})
	req := ListRequest{UID: "u", Limit: 2}
	page, err := app.List(context.Background(), req)
	if err == nil || !reflect.DeepEqual(page, ListResult{}) {
		t.Fatalf("partial page escaped: %+v %v", page, err)
	}
	hydrator.results[1] = HydrationResult{Key: ConversationKey{ChannelID: "bad", ChannelType: 2}, Outcome: HydrationOK, ReadThroughSeq: 2, LastMessage: &LastMessage{MessageSeq: 2}}
	page, err = app.List(context.Background(), req)
	if err != nil || len(page.Items) != 2 || !page.Done {
		t.Fatalf("retry: %+v %v", page, err)
	}
}

func TestListAdmissionRejectsBeforeDirectoryRead(t *testing.T) {
	app := New(Options{Directory: &membershipDirectoryStore{}, Hydrator: &membershipHeadHydrator{}})
	for i := 0; i < cap(app.listAdmission); i++ {
		app.listAdmission <- struct{}{}
	}
	page, err := app.List(context.Background(), ListRequest{UID: "u"})
	if err != ErrListBusy || !reflect.DeepEqual(page, ListResult{}) {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}
