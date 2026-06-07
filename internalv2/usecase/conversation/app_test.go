package conversation

import (
	"context"
	"reflect"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestListSortsJoinedChannelLatestAndPaginatesByConversationCursor(t *testing.T) {
	memberships := &recordingMembershipStore{
		rows: []metadb.UserChannelMembership{
			{UID: "u1", ChannelID: "g-a", ChannelType: 2},
			{UID: "u1", ChannelID: "g-b", ChannelType: 2},
			{UID: "u1", ChannelID: "g-c", ChannelType: 2},
		},
	}
	latest := &recordingLatestStore{rows: map[metadb.ConversationKey]metadb.ChannelLatest{
		{ChannelID: "g-a", ChannelType: 2}: {ChannelID: "g-a", ChannelType: 2, LastMessageID: 10, LastMessageSeq: 10, LastAt: 100, FromUID: "u-a", Payload: []byte("a")},
		{ChannelID: "g-b", ChannelType: 2}: {ChannelID: "g-b", ChannelType: 2, LastMessageID: 30, LastMessageSeq: 30, LastAt: 300, FromUID: "u-b", Payload: []byte("b")},
		{ChannelID: "g-c", ChannelType: 2}: {ChannelID: "g-c", ChannelType: 2, LastMessageID: 20, LastMessageSeq: 20, LastAt: 200, FromUID: "u-c", Payload: []byte("c")},
	}}
	app := New(Options{
		Memberships:         memberships,
		Latest:              latest,
		MembershipPageLimit: 2,
		MaxMembershipScan:   10,
	})

	first, err := app.List(context.Background(), ListRequest{UID: "u1", Limit: 2})
	if err != nil {
		t.Fatalf("List(first) error = %v", err)
	}
	if got, want := conversationIDs(first.Items), []string{"g-b", "g-c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first conversation IDs = %#v, want %#v", got, want)
	}
	if !first.HasMore || first.Truncated {
		t.Fatalf("first HasMore=%v Truncated=%v, want has more without truncation", first.HasMore, first.Truncated)
	}
	if first.NextCursor.ChannelID != "g-c" || first.NextCursor.ChannelType != 2 ||
		first.NextCursor.LastAt != 200 || first.NextCursor.LastMessageSeq != 20 {
		t.Fatalf("first cursor = %#v, want g-c sort cursor", first.NextCursor)
	}
	if got, want := len(latest.calls), 1; got != want {
		t.Fatalf("latest batch calls = %d, want %d", got, want)
	}
	if got, want := latest.calls[0], []metadb.ConversationKey{
		{ChannelID: "g-a", ChannelType: 2},
		{ChannelID: "g-b", ChannelType: 2},
		{ChannelID: "g-c", ChannelType: 2},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("latest batch keys = %#v, want %#v", got, want)
	}

	second, err := app.List(context.Background(), ListRequest{UID: "u1", Cursor: first.NextCursor, Limit: 2})
	if err != nil {
		t.Fatalf("List(second) error = %v", err)
	}
	if got, want := conversationIDs(second.Items), []string{"g-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second conversation IDs = %#v, want %#v", got, want)
	}
	if second.HasMore || second.Truncated {
		t.Fatalf("second HasMore=%v Truncated=%v, want final untruncated page", second.HasMore, second.Truncated)
	}
}

func TestListSkipsMembershipsWithoutLatestAndReportsScanTruncation(t *testing.T) {
	memberships := &recordingMembershipStore{
		rows: []metadb.UserChannelMembership{
			{UID: "u1", ChannelID: "g-a", ChannelType: 2},
			{UID: "u1", ChannelID: "g-b", ChannelType: 2},
			{UID: "u1", ChannelID: "g-c", ChannelType: 2},
		},
	}
	latest := &recordingLatestStore{rows: map[metadb.ConversationKey]metadb.ChannelLatest{
		{ChannelID: "g-b", ChannelType: 2}: {ChannelID: "g-b", ChannelType: 2, LastMessageID: 30, LastMessageSeq: 30, LastAt: 300},
	}}
	app := New(Options{
		Memberships:         memberships,
		Latest:              latest,
		MembershipPageLimit: 2,
		MaxMembershipScan:   2,
	})

	got, err := app.List(context.Background(), ListRequest{UID: "u1", Limit: 50})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if ids := conversationIDs(got.Items); !reflect.DeepEqual(ids, []string{"g-b"}) {
		t.Fatalf("conversation IDs = %#v, want only g-b with latest", ids)
	}
	if !got.Truncated {
		t.Fatalf("Truncated = false, want true when membership scan hits max")
	}
	if got.ScannedMemberships != 2 {
		t.Fatalf("ScannedMemberships = %d, want 2", got.ScannedMemberships)
	}
}

func conversationIDs(items []Conversation) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ChannelID)
	}
	return out
}

type recordingMembershipStore struct {
	rows []metadb.UserChannelMembership
}

func (s *recordingMembershipStore) ListUserChannelMembershipPage(_ context.Context, uid string, after metadb.UserChannelMembershipCursor, limit int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error) {
	start := 0
	for start < len(s.rows) && after != (metadb.UserChannelMembershipCursor{}) {
		row := s.rows[start]
		start++
		if row.ChannelID == after.ChannelID && row.ChannelType == after.ChannelType {
			break
		}
	}
	page := make([]metadb.UserChannelMembership, 0, limit)
	var cursor metadb.UserChannelMembershipCursor
	for start < len(s.rows) && len(page) < limit {
		row := s.rows[start]
		start++
		if row.UID != uid {
			continue
		}
		page = append(page, row)
		cursor = metadb.UserChannelMembershipCursor{ChannelID: row.ChannelID, ChannelType: row.ChannelType}
	}
	return page, cursor, start >= len(s.rows), nil
}

type recordingLatestStore struct {
	rows  map[metadb.ConversationKey]metadb.ChannelLatest
	calls [][]metadb.ConversationKey
}

func (s *recordingLatestStore) GetChannelLatestBatch(_ context.Context, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelLatest, error) {
	s.calls = append(s.calls, append([]metadb.ConversationKey(nil), keys...))
	out := make(map[metadb.ConversationKey]metadb.ChannelLatest)
	for _, key := range keys {
		if latest, ok := s.rows[key]; ok {
			latest.Payload = append([]byte(nil), latest.Payload...)
			out[key] = latest
		}
	}
	return out, nil
}
