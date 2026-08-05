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
			{UID: "u1", ChannelID: "retry", ChannelType: 2, JoinSeq: 1},
		},
		cursor: metadb.UserChannelMembershipCursor{ActivatedAt: 0, ChannelID: "retry", ChannelType: 2},
		done:   false,
	}
	hydrator := &membershipHeadHydrator{results: []HydrationResult{
		{Key: ConversationKey{ChannelID: "visible", ChannelType: 2}, Outcome: HydrationOK, LastCommittedSeq: 12, RetentionThroughSeq: 7, CurrentUserLastSendSeq: 10, LastMessage: &LastMessage{MessageID: 12, MessageSeq: 12, Payload: []byte("last")}},
		{Key: ConversationKey{ChannelID: "active-empty", ChannelType: 2}, Outcome: HydrationNoVisibleMessage, LastCommittedSeq: 19},
		{Key: ConversationKey{ChannelID: "inactive-empty", ChannelType: 2}, Outcome: HydrationNoVisibleMessage, LastCommittedSeq: 29},
		{Key: ConversationKey{ChannelID: "retry", ChannelType: 2}, Outcome: HydrationRetryable},
	}}
	app := New(Options{Directory: directory, Hydrator: hydrator})

	result, err := app.List(context.Background(), ListRequest{UID: "u1", Limit: 5})
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if result.Done || !result.HasMore || result.NextCursor.ChannelID != "retry" {
		t.Fatalf("page state = done=%v hasMore=%v cursor=%+v", result.Done, result.HasMore, result.NextCursor)
	}
	if got, want := result.Deletes, []ConversationKey{{ChannelID: "gone", ChannelType: 2}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deletes = %#v, want %#v", got, want)
	}
	if got, want := result.Unresolved, []ConversationKey{{ChannelID: "retry", ChannelType: 2}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unresolved = %#v, want %#v", got, want)
	}
	if len(result.Items) != 2 || result.Items[0].ChannelID != "visible" || result.Items[0].Unread != 2 || result.Items[0].LastMessage == nil {
		t.Fatalf("visible conversation = %#v", result.Items)
	}
	if result.Items[1].ChannelID != "active-empty" || result.Items[1].Unread != 0 || result.Items[1].LastMessage != nil {
		t.Fatalf("active empty conversation = %#v", result.Items[1])
	}
	if len(hydrator.memberships) != 4 {
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

func TestRetryHydratesOnlyRequestedLiveMembershipsAndReturnsDeletes(t *testing.T) {
	store := &membershipRetryStore{rows: map[ConversationKey]metadb.UserChannelMembership{
		{ChannelID: "visible", ChannelType: 2}:   {UID: "u1", ChannelID: "visible", ChannelType: 2, JoinSeq: 1},
		{ChannelID: "removed", ChannelType: 2}:   {UID: "u1", ChannelID: "removed", ChannelType: 2, Tombstone: true},
		{ChannelID: "retryable", ChannelType: 2}: {UID: "u1", ChannelID: "retryable", ChannelType: 2, JoinSeq: 1},
	}}
	hydrator := &membershipHeadHydrator{results: []HydrationResult{
		{Key: ConversationKey{ChannelID: "visible", ChannelType: 2}, Outcome: HydrationOK, LastCommittedSeq: 3, LastMessage: &LastMessage{MessageSeq: 3}},
		{Key: ConversationKey{ChannelID: "retryable", ChannelType: 2}, Outcome: HydrationRetryable},
	}}
	app := New(Options{Hydrator: hydrator, MembershipMutations: store})

	result, err := app.Retry(context.Background(), RetryRequest{UID: "u1", Keys: []ConversationKey{
		{ChannelID: "visible", ChannelType: 2},
		{ChannelID: "missing", ChannelType: 2},
		{ChannelID: "removed", ChannelType: 2},
		{ChannelID: "retryable", ChannelType: 2},
	}})
	if err != nil {
		t.Fatalf("Retry(): %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ChannelID != "visible" {
		t.Fatalf("items = %#v, want visible", result.Items)
	}
	if got, want := result.Deletes, []ConversationKey{{ChannelID: "missing", ChannelType: 2}, {ChannelID: "removed", ChannelType: 2}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deletes = %#v, want %#v", got, want)
	}
	if got, want := result.Unresolved, []ConversationKey{{ChannelID: "retryable", ChannelType: 2}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unresolved = %#v, want %#v", got, want)
	}
	if len(hydrator.memberships) != 2 {
		t.Fatalf("hydrated memberships = %d, want only two live rows", len(hydrator.memberships))
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

type membershipRetryStore struct {
	rows map[ConversationKey]metadb.UserChannelMembership
}

func (s *membershipRetryStore) GetUserChannelMembership(_ context.Context, _ string, channelID string, channelType int64) (metadb.UserChannelMembership, bool, error) {
	row, ok := s.rows[ConversationKey{ChannelID: channelID, ChannelType: channelType}]
	return row, ok, nil
}

func (*membershipRetryStore) AdvanceUserChannelMembershipReadSeq(context.Context, string, string, int64, uint64, int64) error {
	return nil
}

func (*membershipRetryStore) HideUserChannelMembership(context.Context, string, string, int64, uint64, int64) error {
	return nil
}

func (*membershipRetryStore) ActivateUserChannelMembership(context.Context, string, string, int64, int64, int64) error {
	return nil
}

func (s *membershipHeadHydrator) HydrateConversationHeads(_ context.Context, _ string, memberships []metadb.UserChannelMembership) ([]HydrationResult, error) {
	s.memberships = append([]metadb.UserChannelMembership(nil), memberships...)
	return append([]HydrationResult(nil), s.results...), nil
}
