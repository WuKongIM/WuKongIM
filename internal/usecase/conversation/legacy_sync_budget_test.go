package conversation

import (
	"context"
	"errors"
	"testing"
)

// Budget failures include rejecting a reducible request, dropping selected
// conversations, over-allocating a remainder, overflowing a large request,
// counting off-page/filtered channels, and bypassing the payload-byte guard.
func TestSyncLegacyReducesRecentCountWithinResponseBudget(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		rows, requested, page, pageSize int
		unread, exclude                 bool
		wantChannels, wantLimit         int
	}{
		{name: "within budget", rows: 100, requested: 20, wantChannels: 100, wantLimit: 20},
		{name: "exact budget", rows: 500, requested: 20, wantChannels: 500, wantLimit: 20},
		{name: "over budget", rows: 501, requested: 20, wantChannels: 501, wantLimit: 19},
		{name: "full directory", rows: 1000, requested: 20, wantChannels: 1000, wantLimit: 10},
		{name: "round down", rows: 3, requested: 10000, wantChannels: 3, wantLimit: 3333},
		{name: "huge request", rows: 1, requested: int(^uint(0) >> 1), wantChannels: 1, wantLimit: 10000},
		{name: "page before budget", rows: 1000, requested: 200, page: 2, pageSize: 100, wantChannels: 100, wantLimit: 100},
		{name: "unread before budget", rows: 10, requested: 2000, unread: true, wantChannels: 5, wantLimit: 2000},
		{name: "excluded before budget", rows: 10, requested: 2000, exclude: true, wantChannels: 5, wantLimit: 2000},
		{name: "empty directory", requested: 10000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := prefixRows(tc.rows)
			for i := 0; i < len(rows)/2; i++ {
				if tc.unread {
					rows[i].ReadSeq = 10
				}
				if tc.exclude {
					rows[i].ChannelType = 3
				}
			}
			reader := &budgetRecordingLegacyMessageReader{}
			app := New(Options{Directory: &bulkLegacyDirectory{rows: rows}, Hydrator: &prefixHydrator{}, LegacyMessages: reader})
			req := LegacySyncRequest{UID: "u", MessageCount: tc.requested, Page: tc.page, PageSize: tc.pageSize, OnlyUnread: tc.unread}
			if tc.exclude {
				req.ExcludeChannelTypes = []uint8{3}
			}
			got, err := app.SyncLegacy(context.Background(), req)
			if err != nil {
				t.Fatalf("SyncLegacy(): %v", err)
			}
			if len(got.Items) != tc.wantChannels || len(reader.queries) != tc.wantChannels {
				t.Fatalf("items=%d queries=%d want=%d", len(got.Items), len(reader.queries), tc.wantChannels)
			}
			total := 0
			for i, query := range reader.queries {
				if query.Limit != tc.wantLimit {
					t.Fatalf("query %d limit=%d want=%d", i, query.Limit, tc.wantLimit)
				}
				total += query.Limit
				if got.Items[i].ChannelID != query.ChannelID || got.Items[i].LastMessageSeq != 10 {
					t.Fatalf("item %d lost its selected channel or latest message: %+v", i, got.Items[i])
				}
			}
			if total > legacyRecentMessageBudget {
				t.Fatalf("allocated records=%d exceed budget", total)
			}
		})
	}
}

func TestSyncLegacyReducedRecentCountStillEnforcesPayloadBudget(t *testing.T) {
	payload := make([]byte, legacyRecentPayloadBudget/2)
	reader := &recordingLegacyMessageReader{results: []LegacyMessageReadResult{
		{ChannelID: "g-0000", ChannelType: 2, Messages: []LegacyRecentMessage{{MessageSeq: 10, Payload: payload}}},
		{ChannelID: "g-0001", ChannelType: 2, Messages: []LegacyRecentMessage{{MessageSeq: 10, Payload: payload, StreamData: []byte("x")}}},
	}}
	app := New(Options{Directory: &bulkLegacyDirectory{rows: prefixRows(2)}, Hydrator: &prefixHydrator{}, LegacyMessages: reader})
	got, err := app.SyncLegacy(context.Background(), LegacySyncRequest{UID: "u", MessageCount: 10000})
	if !errors.Is(err, ErrLegacySyncBudgetExceeded) || got.Items != nil || len(reader.queries) != 1 {
		t.Fatalf("result=%+v err=%v reads=%d; payload overflow must fail after the bounded read", got, err, len(reader.queries))
	}
	for _, query := range reader.queries[0] {
		if query.Limit != 5000 {
			t.Fatalf("limit=%d want=5000", query.Limit)
		}
	}
}

type budgetRecordingLegacyMessageReader struct{ queries []LegacyMessageQuery }

func (r *budgetRecordingLegacyMessageReader) ReadLegacyMessagesBatch(ctx context.Context, uid string, queries []LegacyMessageQuery) ([]LegacyMessageReadResult, error) {
	if len(queries) > legacyMessageBatchMaxItems {
		return nil, errors.New("legacy message batch exceeded 200 items")
	}
	r.queries = append(r.queries, queries...)
	return (&prefixMessageReader{}).ReadLegacyMessagesBatch(ctx, uid, queries)
}
