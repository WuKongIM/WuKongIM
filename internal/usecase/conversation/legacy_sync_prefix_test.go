package conversation

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// prefixHydrator returns stable channel data independent of batch boundaries.
type prefixHydrator struct {
	dynamicLegacyHydrator
	seen  []string
	heads map[string]HydrationResult
	fail  string
}

func (h *prefixHydrator) HydratePersistedConversationHeads(_ context.Context, _ string, rows []metadb.UserChannelMembership) ([]HydrationResult, error) {
	out := make([]HydrationResult, 0, len(rows))
	for _, row := range rows {
		h.seen = append(h.seen, row.ChannelID)
		if row.ChannelID == h.fail {
			return nil, errPrefixDisk
		}
		head, ok := h.heads[row.ChannelID]
		if !ok {
			head = HydrationResult{Outcome: HydrationOK, ReadThroughSeq: 10, LastMessage: &LastMessage{MessageSeq: 10, ClientMsgNo: "tail"}}
		}
		head.Key = ConversationKey{ChannelID: row.ChannelID, ChannelType: row.ChannelType}
		out = append(out, head)
	}
	return out, nil
}

var errPrefixDisk = errors.New("prefix test disk unavailable")

func prefixRows(n int) []metadb.UserChannelMembership {
	rows := make([]metadb.UserChannelMembership, n)
	for i := range rows {
		rows[i] = metadb.UserChannelMembership{UID: "u", ChannelID: fmt.Sprintf("g-%04d", i), ChannelType: 2, JoinSeq: 1}
	}
	return rows
}

func TestSyncLegacyReadsOnlyRequestedVisiblePrefix(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		page, size, wantRead, wantItems int
	}{
		{"first", 1, 100, 100, 100}, {"second", 2, 100, 200, 100},
		{"default size", 1, 0, 100, 100}, {"clamped size", 1, 999, 500, 500},
		{"unpaged", 0, 100, 1000, 1000}, {"negative page", -1, 100, 1000, 1000},
		{"past scan budget", 11, 100, 1000, 0}, {"huge page", int(^uint(0) >> 1), 100, 1000, 0},
		{"overflow cannot wrap to first page", int(^uint(0)>>1)/2 + 2, 4, 1000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &prefixHydrator{}
			a := New(Options{Directory: &bulkLegacyDirectory{rows: prefixRows(1200)}, Hydrator: h, LegacyMessages: &prefixMessageReader{}})
			got, err := a.SyncLegacy(context.Background(), LegacySyncRequest{UID: "u", MessageCount: 1, Page: tc.page, PageSize: tc.size})
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Items) != tc.wantItems {
				t.Fatalf("items=%d want=%d", len(got.Items), tc.wantItems)
			}
			if len(h.seen) != tc.wantRead {
				t.Fatalf("hydrated=%d want=%d", len(h.seen), tc.wantRead)
			}
			if tc.page == 2 && got.Items[0].ChannelID != "g-0100" {
				t.Fatalf("second page starts at %s", got.Items[0].ChannelID)
			}
		})
	}
}

func TestSyncLegacyPrefixPreservesVisibilityAndPostPageFilters(t *testing.T) {
	// Equal activation times exercise ChannelID ordering. Invisible directory
	// candidates precede an activated-empty row and ordinary visible rows.
	rows := prefixRows(10)
	rows[0].Tombstone = true
	rows[1].ConversationHiddenThroughSeq = 10
	rows[3].DeletedToSeq = 10
	rows[4].ActivatedAt = 0
	rows[5].ReadSeq = 10
	rows[6].ChannelType = 3
	heads := map[string]HydrationResult{
		"g-0002": {Outcome: HydrationDelete},
		"g-0004": {Outcome: HydrationNoVisibleMessage},
	}
	for _, tc := range []struct {
		name string
		req  LegacySyncRequest
		want []string
		read int
	}{
		{"replenish invisible", LegacySyncRequest{Page: 1, PageSize: 2}, []string{"g-0005", "g-0006"}, 6},
		{"second page", LegacySyncRequest{Page: 2, PageSize: 2}, []string{"g-0007", "g-0008"}, 8},
		{"unread after page", LegacySyncRequest{Page: 1, PageSize: 2, OnlyUnread: true}, []string{"g-0006"}, 6},
		{"excluded after page", LegacySyncRequest{Page: 1, PageSize: 2, ExcludeChannelTypes: []uint8{3}}, []string{"g-0005"}, 6},
		{"client override", LegacySyncRequest{Page: 1, PageSize: 2, OnlyUnread: true, ExcludeChannelTypes: []uint8{3}, ClientLastMessageSeqs: []LegacyConversationCursor{{ChannelID: "g-0005", ChannelType: 2, LastMessageSeq: 9}, {ChannelID: "g-0006", ChannelType: 3, LastMessageSeq: 9}}}, []string{"g-0005", "g-0006"}, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &prefixHydrator{heads: heads}
			a := New(Options{Directory: &bulkLegacyDirectory{rows: rows}, Hydrator: h, LegacyMessages: &prefixMessageReader{}})
			tc.req.UID = "u"
			tc.req.MessageCount = 1
			got, err := a.SyncLegacy(context.Background(), tc.req)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, v := range got.Items {
				ids = append(ids, v.ChannelID)
			}
			if !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("ids=%v want=%v", ids, tc.want)
			}
			if len(h.seen) != tc.read {
				t.Fatalf("hydrated=%d want=%d", len(h.seen), tc.read)
			}
		})
	}
}

func TestSyncLegacyPrefixDoesNotRefillActivatedEmptyOrEmptyRecents(t *testing.T) {
	for _, emptyHead := range []bool{true, false} {
		rows := prefixRows(3)
		rows[0].ActivatedAt = 1
		heads := map[string]HydrationResult{}
		if emptyHead {
			heads[rows[0].ChannelID] = HydrationResult{Outcome: HydrationNoVisibleMessage}
		}
		h := &prefixHydrator{heads: heads}
		a := New(Options{Directory: &bulkLegacyDirectory{rows: rows}, Hydrator: h, LegacyMessages: &recordingLegacyMessageReader{results: []LegacyMessageReadResult{{ChannelID: rows[0].ChannelID, ChannelType: 2}}}})
		got, err := a.SyncLegacy(context.Background(), LegacySyncRequest{UID: "u", MessageCount: 1, Page: 1, PageSize: 1})
		if err != nil || len(got.Items) != 0 || len(h.seen) != 1 {
			t.Fatalf("emptyHead=%v result=%+v err=%v reads=%v", emptyHead, got, err, h.seen)
		}
	}
}

func TestSyncLegacyPrefixFailsNeededReadsButDoesNotReadLaterPage(t *testing.T) {
	for _, page := range []int{1, 2} {
		h := &prefixHydrator{fail: "g-0001"}
		a := New(Options{Directory: &bulkLegacyDirectory{rows: prefixRows(3)}, Hydrator: h, LegacyMessages: &prefixMessageReader{}})
		got, err := a.SyncLegacy(context.Background(), LegacySyncRequest{UID: "u", MessageCount: 1, Page: page, PageSize: 1})
		if page == 1 {
			if err != nil || len(got.Items) != 1 {
				t.Fatalf("first page result=%+v err=%v", got, err)
			}
		} else if !errors.Is(err, errPrefixDisk) || got.Items != nil {
			t.Fatalf("needed failure returned partial success: %+v %v", got, err)
		}
	}
}

func TestSyncLegacyPrefixHiddenCandidatesStillConsumeScanBudget(t *testing.T) {
	rows := prefixRows(1200)
	for i := 0; i < 999; i++ {
		rows[i].Tombstone = true
	}
	h := &prefixHydrator{}
	a := New(Options{Directory: &bulkLegacyDirectory{rows: rows}, Hydrator: h, LegacyMessages: &prefixMessageReader{}})
	got, err := a.SyncLegacy(context.Background(), LegacySyncRequest{UID: "u", MessageCount: 1, Page: 1, PageSize: 2})
	if err != nil || len(got.Items) != 1 || got.Items[0].ChannelID != "g-0999" || len(h.seen) != 1 {
		t.Fatalf("result=%+v err=%v reads=%v", got, err, h.seen)
	}
}

// Compare page contents with the previous full bounded candidate walk on stable
// data; hydration must not depend on how directory batches are partitioned.
func TestLegacyCandidatePrefixMatchesBoundedFullScan(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for fixture := 0; fixture < 12; fixture++ {
		rows := prefixRows(1100)
		heads := make(map[string]HydrationResult)
		for i := range rows {
			rows[i].ActivatedAt = int64(rng.Intn(4))
			rows[i].ChannelType = int64(1 + rng.Intn(3))
			switch rng.Intn(7) {
			case 0:
				rows[i].Tombstone = true
			case 1:
				heads[rows[i].ChannelID] = HydrationResult{Outcome: HydrationDelete}
			case 2:
				heads[rows[i].ChannelID] = HydrationResult{Outcome: HydrationNoVisibleMessage}
			case 3:
				rows[i].ConversationHiddenThroughSeq = 10
			}
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].ActivatedAt != rows[j].ActivatedAt {
				return rows[i].ActivatedAt > rows[j].ActivatedAt
			}
			if rows[i].ChannelID != rows[j].ChannelID {
				return rows[i].ChannelID < rows[j].ChannelID
			}
			return rows[i].ChannelType < rows[j].ChannelType
		})
		app := New(Options{Directory: &bulkLegacyDirectory{rows: rows}, Hydrator: &prefixHydrator{heads: heads}, LegacyMessages: &prefixMessageReader{}})
		full, err := app.listLegacyConversationCandidates(context.Background(), "u", legacyConversationSyncMaxCandidates)
		if err != nil {
			t.Fatal(err)
		}
		for _, page := range []int{1, 2, 5, 20} {
			for _, size := range []int{1, 25, 100, 200, 500} {
				prefix, err := app.listLegacyConversationCandidates(context.Background(), "u", legacyConversationPrefixLimit(page, size))
				if err != nil {
					t.Fatal(err)
				}
				got, want := legacyConversationPage(prefix, page, size), legacyConversationPage(full, page, size)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("fixture=%d page=%d size=%d does not match full scan", fixture, page, size)
				}
			}
		}
	}
}

// prefixMessageReader mirrors a stable persisted tail and its exclusive cursor.
type prefixMessageReader struct{}

func (*prefixMessageReader) ReadLegacyMessagesBatch(_ context.Context, _ string, queries []LegacyMessageQuery) ([]LegacyMessageReadResult, error) {
	results := make([]LegacyMessageReadResult, len(queries))
	for i, q := range queries {
		results[i] = LegacyMessageReadResult{ChannelID: q.ChannelID, ChannelType: q.ChannelType}
		if q.AfterMessageSeq < 10 && q.Limit > 0 {
			results[i].Messages = []LegacyRecentMessage{{MessageSeq: 10, ClientMsgNo: "tail", ChannelID: q.ChannelID, ChannelType: q.ChannelType}}
		}
	}
	return results, nil
}

// The durable string index compares encoded length before bytes; legacy sync
// compares the complete bounded directory by string value before paging.
func TestSyncLegacyPrefixSortsVariableLengthDirectoryBeforePaging(t *testing.T) {
	rows := []metadb.UserChannelMembership{
		{UID: "u", ChannelID: "b", ChannelType: 2, JoinSeq: 1},
		{UID: "u", ChannelID: "aa", ChannelType: 2, JoinSeq: 1},
	}
	h := &prefixHydrator{}
	a := New(Options{Directory: &bulkLegacyDirectory{rows: rows}, Hydrator: h, LegacyMessages: &prefixMessageReader{}})
	got, err := a.SyncLegacy(context.Background(), LegacySyncRequest{UID: "u", MessageCount: 1, Page: 1, PageSize: 1})
	if err != nil || len(got.Items) != 1 || got.Items[0].ChannelID != "aa" || !reflect.DeepEqual(h.seen, []string{"aa"}) {
		t.Fatalf("result=%+v err=%v reads=%v", got, err, h.seen)
	}
}

// Even an early output page needs the bounded metadata set to establish order.
func TestSyncLegacyPrefixFailsLaterMetadataRead(t *testing.T) {
	h := &prefixHydrator{}
	a := New(Options{Directory: &failingPrefixDirectory{bulkLegacyDirectory{rows: prefixRows(300)}}, Hydrator: h, LegacyMessages: &prefixMessageReader{}})
	got, err := a.SyncLegacy(context.Background(), LegacySyncRequest{UID: "u", MessageCount: 1, Page: 1, PageSize: 1})
	if !errors.Is(err, errPrefixDisk) || got.Items != nil || len(h.seen) != 0 {
		t.Fatalf("result=%+v err=%v reads=%v", got, err, h.seen)
	}
}

type failingPrefixDirectory struct{ bulkLegacyDirectory }

func (d *failingPrefixDirectory) ListUserChannelMembershipPage(ctx context.Context, uid string, after metadb.UserChannelMembershipCursor, limit int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error) {
	if after.ChannelID != "" {
		return nil, metadb.UserChannelMembershipCursor{}, false, errPrefixDisk
	}
	return d.bulkLegacyDirectory.ListUserChannelMembershipPage(ctx, uid, after, limit)
}
