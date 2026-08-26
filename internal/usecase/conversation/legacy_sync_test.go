package conversation

import (
	"context"
	"errors"
	"fmt"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestSyncLegacyBuildsOldConversationFromDirectoryAndRecentMessages(t *testing.T) {
	directory := &membershipDirectoryStore{
		rows: []metadb.UserChannelMembership{{
			UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 1,
			ReadSeq: 7, ActivatedAt: 100, UpdatedAt: 101,
		}},
		done: true,
	}
	hydrator := &membershipHeadHydrator{results: []HydrationResult{{
		Key: ConversationKey{ChannelID: "g1", ChannelType: 2}, Outcome: HydrationOK,
		LastCommittedSeq: 9,
		LastMessage: &LastMessage{
			MessageID: 99, MessageSeq: 9, FromUID: "u2", ClientMsgNo: "client-9",
			ServerTimestampMS: 1_700_000_000_000, Payload: []byte("nine"),
		},
	}}}
	messages := &recordingLegacyMessageReader{results: []LegacyMessageReadResult{{
		ChannelID: "g1", ChannelType: 2,
		Messages: []LegacyRecentMessage{
			{MessageID: 88, MessageSeq: 8, ChannelID: "g1", ChannelType: 2, Timestamp: 1_699_999_999, Payload: []byte("eight")},
			{MessageID: 99, MessageSeq: 9, ChannelID: "g1", ChannelType: 2, ClientMsgNo: "client-9", Timestamp: 1_700_000_000, Payload: []byte("nine")},
		},
	}}}
	app := New(Options{Directory: directory, Hydrator: hydrator, LegacyMessages: messages})

	result, err := app.SyncLegacy(context.Background(), LegacySyncRequest{
		UID: "u1", MessageCount: 2,
		ClientLastMessageSeqs: []LegacyConversationCursor{{ChannelID: "g1", ChannelType: 2, LastMessageSeq: 7}},
	})
	if err != nil {
		t.Fatalf("SyncLegacy(): %v", err)
	}
	if len(messages.queries) != 1 || len(messages.queries[0]) != 1 {
		t.Fatalf("message queries = %#v, want one batch item", messages.queries)
	}
	query := messages.queries[0][0]
	if query.ChannelID != "g1" || query.ChannelType != 2 || query.AfterMessageSeq != 7 || query.Limit != 2 {
		t.Fatalf("message query = %#v, want newest two messages after seq 7", query)
	}
	if len(result.Items) != 1 {
		t.Fatalf("result = %#v, want one conversation", result)
	}
	item := result.Items[0]
	if item.ChannelID != "g1" || item.Unread != 2 || item.LastMessageSeq != 9 || item.LastClientMsgNo != "client-9" || item.ReadToMessageSeq != 7 || item.Timestamp != 1_700_000_000 || item.Version != 1_700_000_000_000_000_000 {
		t.Fatalf("legacy item = %#v", item)
	}
	if len(item.Recents) != 2 || item.Recents[0].MessageSeq != 9 || item.Recents[1].MessageSeq != 8 {
		t.Fatalf("recents = %#v, want newest first", item.Recents)
	}
}

func TestSyncLegacyAppliesOldPageUnreadAndExcludedTypeRules(t *testing.T) {
	directory := &membershipDirectoryStore{
		rows: []metadb.UserChannelMembership{
			{UID: "u1", ChannelID: "first", ChannelType: 2, JoinSeq: 1, ActivatedAt: 300},
			{UID: "u1", ChannelID: "excluded-but-known", ChannelType: 3, JoinSeq: 1, ActivatedAt: 200},
			{UID: "u1", ChannelID: "read", ChannelType: 2, JoinSeq: 1, ReadSeq: 7, ActivatedAt: 100},
		},
		done: true,
	}
	hydrator := &membershipHeadHydrator{results: []HydrationResult{
		{Key: ConversationKey{ChannelID: "first", ChannelType: 2}, Outcome: HydrationOK, LastCommittedSeq: 5, LastMessage: &LastMessage{MessageSeq: 5}},
		{Key: ConversationKey{ChannelID: "excluded-but-known", ChannelType: 3}, Outcome: HydrationOK, LastCommittedSeq: 6, LastMessage: &LastMessage{MessageSeq: 6}},
		{Key: ConversationKey{ChannelID: "read", ChannelType: 2}, Outcome: HydrationOK, LastCommittedSeq: 7, LastMessage: &LastMessage{MessageSeq: 7}},
	}}
	messages := &recordingLegacyMessageReader{results: []LegacyMessageReadResult{{
		ChannelID: "excluded-but-known", ChannelType: 3,
		Messages: []LegacyRecentMessage{{MessageSeq: 6, ChannelID: "excluded-but-known", ChannelType: 3}},
	}}}
	app := New(Options{Directory: directory, Hydrator: hydrator, LegacyMessages: messages})

	result, err := app.SyncLegacy(context.Background(), LegacySyncRequest{
		UID: "u1", MessageCount: 5, OnlyUnread: true, Page: 2, PageSize: 1,
		ExcludeChannelTypes: []uint8{3},
		ClientLastMessageSeqs: []LegacyConversationCursor{{
			ChannelID: "excluded-but-known", ChannelType: 3, LastMessageSeq: 5,
		}},
	})
	if err != nil {
		t.Fatalf("SyncLegacy(): %v", err)
	}
	if len(messages.queries) != 1 || len(messages.queries[0]) != 1 {
		t.Fatalf("message queries = %#v, want only the second page row", messages.queries)
	}
	query := messages.queries[0][0]
	if query.ChannelID != "excluded-but-known" || query.AfterMessageSeq != 5 {
		t.Fatalf("query = %#v, want client-known excluded type override", query)
	}
	if len(result.Items) != 1 || result.Items[0].ChannelID != "excluded-but-known" {
		t.Fatalf("result = %#v, want overridden excluded conversation", result)
	}
}

func TestSyncLegacyWalksDirectoryPagesForOldUnpagedRequest(t *testing.T) {
	directory := &pagedLegacyDirectory{rows: []metadb.UserChannelMembership{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 1, ActivatedAt: 200},
		{UID: "u1", ChannelID: "g2", ChannelType: 2, JoinSeq: 1, ActivatedAt: 100},
	}}
	hydrator := dynamicLegacyHydrator{}
	messages := &echoLegacyMessageReader{}
	app := New(Options{Directory: directory, Hydrator: hydrator, LegacyMessages: messages})

	result, err := app.SyncLegacy(context.Background(), LegacySyncRequest{UID: "u1", MessageCount: 1})
	if err != nil {
		t.Fatalf("SyncLegacy(): %v", err)
	}
	if directory.calls != 2 {
		t.Fatalf("directory calls = %d, want complete two-page walk", directory.calls)
	}
	if len(result.Items) != 2 || result.Items[0].ChannelID != "g1" || result.Items[1].ChannelID != "g2" {
		t.Fatalf("result = %#v, want both directory pages in order", result)
	}
}

func TestSyncLegacyRetriesUnresolvedWithoutSilentlyDroppingConversation(t *testing.T) {
	row := metadb.UserChannelMembership{UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 1, ActivatedAt: 100}
	directory := &membershipDirectoryStore{rows: []metadb.UserChannelMembership{row}, done: true}
	memberships := &membershipRetryStore{rows: map[ConversationKey]metadb.UserChannelMembership{
		{ChannelID: "g1", ChannelType: 2}: row,
	}}
	hydrator := &retryOnceLegacyHydrator{}
	app := New(Options{
		Directory: directory, Hydrator: hydrator, MembershipMutations: memberships,
		LegacyMessages: &echoLegacyMessageReader{},
	})

	result, err := app.SyncLegacy(context.Background(), LegacySyncRequest{UID: "u1", MessageCount: 1})
	if err != nil {
		t.Fatalf("SyncLegacy(): %v", err)
	}
	if hydrator.calls != 2 {
		t.Fatalf("hydrator calls = %d, want list plus unresolved retry", hydrator.calls)
	}
	if len(result.Items) != 1 || result.Items[0].ChannelID != "g1" {
		t.Fatalf("result = %#v, want recovered conversation", result)
	}

	persistent := &alwaysUnresolvedLegacyHydrator{}
	app = New(Options{
		Directory: directory, Hydrator: persistent, MembershipMutations: memberships,
		LegacyMessages: &echoLegacyMessageReader{},
	})
	_, err = app.SyncLegacy(context.Background(), LegacySyncRequest{UID: "u1", MessageCount: 1})
	if !errors.Is(err, ErrLegacySyncUnresolved) {
		t.Fatalf("SyncLegacy() error = %v, want ErrLegacySyncUnresolved", err)
	}
}

func TestSyncLegacyBoundsRecentMessageBatches(t *testing.T) {
	rows := make([]metadb.UserChannelMembership, 201)
	for index := range rows {
		rows[index] = metadb.UserChannelMembership{
			UID: "u1", ChannelID: fmt.Sprintf("g-%03d", index), ChannelType: 2,
			JoinSeq: 1, ActivatedAt: int64(1000 - index),
		}
	}
	directory := &bulkLegacyDirectory{rows: rows}
	messages := &boundedEchoLegacyMessageReader{}
	app := New(Options{Directory: directory, Hydrator: dynamicLegacyHydrator{}, LegacyMessages: messages})

	result, err := app.SyncLegacy(context.Background(), LegacySyncRequest{UID: "u1", MessageCount: 1})
	if err != nil {
		t.Fatalf("SyncLegacy(): %v", err)
	}
	if len(result.Items) != 201 {
		t.Fatalf("items = %d, want 201", len(result.Items))
	}
	if len(messages.batchSizes) != 2 || messages.batchSizes[0] != 200 || messages.batchSizes[1] != 1 {
		t.Fatalf("message batch sizes = %#v, want [200 1]", messages.batchSizes)
	}
}

func TestSyncLegacyUsesEffectiveReadFloorForUnreadAndVersionRequests(t *testing.T) {
	for _, test := range []struct {
		name         string
		onlyUnread   bool
		version      int64
		clientCursor []LegacyConversationCursor
	}{
		{name: "only unread", onlyUnread: true},
		{name: "version present", version: 1},
		{
			name: "zero client cursor still uses the read floor", version: 1,
			clientCursor: []LegacyConversationCursor{{
				ChannelID: "g1", ChannelType: 2, LastMessageSeq: 0,
			}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := &membershipDirectoryStore{rows: []metadb.UserChannelMembership{{
				UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 1, ReadSeq: 3, ActivatedAt: 100,
			}}, done: true}
			hydrator := &membershipHeadHydrator{results: []HydrationResult{{
				Key: ConversationKey{ChannelID: "g1", ChannelType: 2}, Outcome: HydrationOK,
				LastCommittedSeq: 9, CurrentUserLastSendSeq: 7,
				LastMessage: &LastMessage{MessageSeq: 9},
			}}}
			messages := &recordingLegacyMessageReader{results: []LegacyMessageReadResult{{
				ChannelID: "g1", ChannelType: 2,
				Messages: []LegacyRecentMessage{{MessageSeq: 9, ChannelID: "g1", ChannelType: 2}},
			}}}
			app := New(Options{Directory: directory, Hydrator: hydrator, LegacyMessages: messages})

			result, err := app.SyncLegacy(context.Background(), LegacySyncRequest{
				UID: "u1", MessageCount: 10, OnlyUnread: test.onlyUnread, Version: test.version,
				ClientLastMessageSeqs: test.clientCursor,
			})
			if err != nil {
				t.Fatalf("SyncLegacy(): %v", err)
			}
			if len(messages.queries) != 1 || len(messages.queries[0]) != 1 || messages.queries[0][0].AfterMessageSeq != 7 {
				t.Fatalf("queries = %#v, want effective read floor 7", messages.queries)
			}
			if len(result.Items) != 1 || result.Items[0].ReadToMessageSeq != 7 || result.Items[0].Unread != 2 {
				t.Fatalf("result = %#v, want effective read=7 unread=2", result)
			}
		})
	}
}

type recordingLegacyMessageReader struct {
	queries [][]LegacyMessageQuery
	results []LegacyMessageReadResult
	err     error
}

func (r *recordingLegacyMessageReader) ReadLegacyMessagesBatch(_ context.Context, _ string, queries []LegacyMessageQuery) ([]LegacyMessageReadResult, error) {
	r.queries = append(r.queries, append([]LegacyMessageQuery(nil), queries...))
	return append([]LegacyMessageReadResult(nil), r.results...), r.err
}

type pagedLegacyDirectory struct {
	rows  []metadb.UserChannelMembership
	calls int
}

func (d *pagedLegacyDirectory) ListUserChannelMembershipPage(_ context.Context, _ string, after metadb.UserChannelMembershipCursor, _ int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error) {
	d.calls++
	start := 0
	if after.ChannelID != "" {
		for index, row := range d.rows {
			if row.ChannelID == after.ChannelID && row.ChannelType == after.ChannelType {
				start = index + 1
				break
			}
		}
	}
	if start >= len(d.rows) {
		return []metadb.UserChannelMembership{}, metadb.UserChannelMembershipCursor{}, true, nil
	}
	row := d.rows[start]
	done := start+1 == len(d.rows)
	return []metadb.UserChannelMembership{row}, metadb.UserChannelMembershipCursor{
		ActivatedAt: row.ActivatedAt, ChannelID: row.ChannelID, ChannelType: row.ChannelType,
	}, done, nil
}

type dynamicLegacyHydrator struct{}

func (dynamicLegacyHydrator) HydrateConversationHeads(_ context.Context, _ string, memberships []metadb.UserChannelMembership) ([]HydrationResult, error) {
	results := make([]HydrationResult, len(memberships))
	for index, row := range memberships {
		results[index] = HydrationResult{
			Key:     ConversationKey{ChannelID: row.ChannelID, ChannelType: row.ChannelType},
			Outcome: HydrationOK, LastCommittedSeq: uint64(index + 1),
			LastMessage: &LastMessage{MessageSeq: uint64(index + 1)},
		}
	}
	return results, nil
}

type echoLegacyMessageReader struct{}

func (*echoLegacyMessageReader) ReadLegacyMessagesBatch(_ context.Context, _ string, queries []LegacyMessageQuery) ([]LegacyMessageReadResult, error) {
	results := make([]LegacyMessageReadResult, len(queries))
	for index, query := range queries {
		results[index] = LegacyMessageReadResult{
			ChannelID: query.ChannelID, ChannelType: query.ChannelType,
			Messages: []LegacyRecentMessage{{
				MessageSeq: uint64(index + 1), ChannelID: query.ChannelID, ChannelType: query.ChannelType,
			}},
		}
	}
	return results, nil
}

type retryOnceLegacyHydrator struct {
	calls int
}

func (h *retryOnceLegacyHydrator) HydrateConversationHeads(_ context.Context, _ string, memberships []metadb.UserChannelMembership) ([]HydrationResult, error) {
	h.calls++
	result := HydrationResult{Key: ConversationKey{ChannelID: memberships[0].ChannelID, ChannelType: memberships[0].ChannelType}}
	if h.calls == 1 {
		result.Outcome = HydrationRetryable
	} else {
		result.Outcome = HydrationOK
		result.LastCommittedSeq = 1
		result.LastMessage = &LastMessage{MessageSeq: 1}
	}
	return []HydrationResult{result}, nil
}

type alwaysUnresolvedLegacyHydrator struct{}

func (*alwaysUnresolvedLegacyHydrator) HydrateConversationHeads(_ context.Context, _ string, memberships []metadb.UserChannelMembership) ([]HydrationResult, error) {
	return []HydrationResult{{
		Key:     ConversationKey{ChannelID: memberships[0].ChannelID, ChannelType: memberships[0].ChannelType},
		Outcome: HydrationRetryable,
	}}, nil
}

type bulkLegacyDirectory struct {
	rows []metadb.UserChannelMembership
}

func (d *bulkLegacyDirectory) ListUserChannelMembershipPage(_ context.Context, _ string, after metadb.UserChannelMembershipCursor, limit int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error) {
	start := 0
	if after.ChannelID != "" {
		for index, row := range d.rows {
			if row.ChannelID == after.ChannelID && row.ChannelType == after.ChannelType {
				start = index + 1
				break
			}
		}
	}
	if start >= len(d.rows) {
		return []metadb.UserChannelMembership{}, metadb.UserChannelMembershipCursor{}, true, nil
	}
	end := min(start+limit, len(d.rows))
	page := append([]metadb.UserChannelMembership(nil), d.rows[start:end]...)
	last := page[len(page)-1]
	return page, metadb.UserChannelMembershipCursor{
		ActivatedAt: last.ActivatedAt, ChannelID: last.ChannelID, ChannelType: last.ChannelType,
	}, end == len(d.rows), nil
}

type boundedEchoLegacyMessageReader struct {
	batchSizes []int
}

func (r *boundedEchoLegacyMessageReader) ReadLegacyMessagesBatch(ctx context.Context, uid string, queries []LegacyMessageQuery) ([]LegacyMessageReadResult, error) {
	r.batchSizes = append(r.batchSizes, len(queries))
	if len(queries) > 200 {
		return nil, errors.New("legacy message batch exceeded 200 items")
	}
	return (&echoLegacyMessageReader{}).ReadLegacyMessagesBatch(ctx, uid, queries)
}
