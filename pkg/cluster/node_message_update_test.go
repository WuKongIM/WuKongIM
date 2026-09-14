package cluster

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	store "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"testing"
)

func TestMessageUpdateGrowthPreservesPageContinuation(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		msgs := []ch.Message{{MessageSeq: 10, Payload: []byte("longer")}, {MessageSeq: 11, Payload: []byte("longer")}}
		if reverse {
			msgs[0].MessageSeq = 11
			msgs[1].MessageSeq = 10
		}
		reads := []channels.CommittedRead{{Request: store.ReadCommittedRequest{MaxBytes: 6, Reverse: reverse}}}
		results := []channels.CommittedReadResult{{Read: store.ReadCommittedResult{Messages: msgs}}}
		if err := (&Node{}).overlayMessageReads(context.Background(), reads, results); err != nil {
			t.Fatal(err)
		}
		result := results[0]
		want := uint64(11)
		if reverse {
			want = 10
		}
		if len(result.Read.Messages) != 1 || !result.ContentTruncated || result.Read.NextSeq != want {
			t.Fatalf("reverse=%v result=%+v", reverse, result)
		}
	}
}

// A page of 100 conversations with three recents must retain cross-channel
// batching after crossing the 200-record bound, rather than doing 100 barriers.
func TestMessageUpdateWideRecentsStayBatched(t *testing.T) {
	reads := make([]channels.CommittedRead, 100)
	results := make([]channels.CommittedReadResult, 100)
	for i := range results {
		results[i].Read.Messages = []ch.Message{{MessageSeq: 1}, {MessageSeq: 2}, {MessageSeq: 3}}
	}
	calls := 0
	err := overlayMessageReadResults(context.Background(), reads, results, func(_ context.Context, messages []*ch.Message) error {
		calls++
		if len(messages) > 200 {
			t.Fatalf("unbounded batch: %d", len(messages))
		}
		for _, m := range messages {
			m.Payload = []byte("edited")
			m.Version = 1
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("300 recents require two bounded batches, got %d", calls)
	}
	for _, r := range results {
		for _, m := range r.Read.Messages {
			if m.Version != 1 {
				t.Fatal("missed overlay")
			}
		}
	}
}

func TestMessageUpdateWideBatchGrowthBoundsAndContinuation(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		reads := []channels.CommittedRead{{Request: store.ReadCommittedRequest{MaxBytes: 2 << 20, Reverse: reverse}}, {Request: store.ReadCommittedRequest{MaxBytes: 3 << 20, Reverse: reverse}}}
		results := make([]channels.CommittedReadResult, 2)
		for row := range results {
			for i := 0; i < 210; i++ {
				seq := uint64(i + 1)
				if reverse {
					seq = uint64(210 - i)
				}
				results[row].Read.Messages = append(results[row].Read.Messages, ch.Message{MessageSeq: seq})
			}
		}
		calls := 0
		err := overlayMessageReadResults(context.Background(), reads, results, func(_ context.Context, messages []*ch.Message) error {
			calls++
			if len(messages) > 7 {
				return metadb.ErrInvalidArgument
			}
			for _, m := range messages {
				m.Payload = make([]byte, 1<<20)
				m.Version = 1
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if calls != 3 {
			t.Fatalf("must skip truncated rows and bound retries, got %d calls", calls)
		}
		for row, r := range results {
			want := uint64(row + 3)
			if reverse {
				want = uint64(208 - row)
			}
			if len(r.Read.Messages) != row+2 || !r.ContentTruncated || r.Read.NextSeq != want {
				t.Fatalf("reverse=%v row=%d count=%d next=%d", reverse, row, len(r.Read.Messages), r.Read.NextSeq)
			}
		}
	}
}

func TestMessageUpdateEmptyOverlayNeedsNoJoinAllocations(t *testing.T) {
	messages := []*ch.Message{{ChannelID: "a", ChannelType: 2, MessageID: 1, MessageSeq: 1}}
	pages := []metadb.MessageUpdatePage{{}}
	groups := map[metadb.ChannelKey]int{{ChannelID: "a", ChannelType: 2}: 0}
	if got := testing.AllocsPerRun(100, func() { applyMessageUpdatePages(messages, pages, groups) }); got != 0 {
		t.Fatalf("empty overlay must not build join maps: %g allocations", got)
	}
}

func TestMessageUpdatePageJoinPreservesChannelAndSequenceIdentity(t *testing.T) {
	for _, size := range []int{1, 9, 200} {
		messages := []*ch.Message{{ChannelID: "a", ChannelType: 2, MessageID: 1, MessageSeq: 1}, {ChannelID: "b", ChannelType: 2, MessageID: 1, MessageSeq: 1}, {ChannelID: "a", ChannelType: 2, MessageID: 1, MessageSeq: 999, Payload: []byte("original")}, {ChannelID: "a", ChannelType: 2, MessageID: 1, MessageSeq: 1}}
		reads := []metadb.MessageUpdateRead{{ChannelID: "a", ChannelType: 2}, {ChannelID: "b", ChannelType: 2}}
		pages := make([]metadb.MessageUpdatePage, 2)
		for i := range pages {
			for j := 0; j < size; j++ {
				pages[i].Updates = append(pages[i].Updates, metadb.MessageUpdate{MessageID: uint64(j + 1), MessageSeq: uint64(j + 1), Version: 2, UpdatedAtMS: 123, Payload: []byte(reads[i].ChannelID)})
			}
		}
		groups := map[metadb.ChannelKey]int{{ChannelID: "a", ChannelType: 2}: 0, {ChannelID: "b", ChannelType: 2}: 1}
		applyMessageUpdatePages(messages, pages, groups)
		for _, i := range []int{0, 1, 3} {
			if string(messages[i].Payload) != messages[i].ChannelID || messages[i].Version != 2 || messages[i].UpdatedAtMS != 123 {
				t.Fatalf("size=%d incorrect match %d", size, i)
			}
		}
		if string(messages[2].Payload) != "original" || messages[2].Version != 0 {
			t.Fatalf("size=%d overlaid a different retained sequence", size)
		}
	}
}
