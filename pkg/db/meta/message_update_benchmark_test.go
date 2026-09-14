package meta

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkMessageUpdateIncrementalIndex verifies that fetching the last edit
// does not scale with the retained history preceding the channel cursor.
func BenchmarkMessageUpdateIncrementalIndex(b *testing.B) {
	for _, size := range []int{1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			db, err := Open(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			ctx := context.Background()
			wb := db.NewWriteBatch()
			defer wb.Close()
			if err = wb.UpsertChannel(0, Channel{ChannelID: "g", ChannelType: 2}); err != nil {
				b.Fatal(err)
			}
			if err = wb.UpsertChannelRuntimeMeta(0, ChannelRuntimeMeta{ChannelID: "g", ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, Leader: 1, MinISR: 1, Replicas: []uint64{1}, ISR: []uint64{1}}); err != nil {
				b.Fatal(err)
			}
			if _, err = wb.ApplyMessageUpdate(0, MessageUpdateMutation{Op: "init", ChannelID: "g", ChannelType: 2, Generation: "g"}); err != nil {
				b.Fatal(err)
			}
			if err = wb.Commit(); err != nil {
				b.Fatal(err)
			}
			for start := 1; start <= size; start += 100 {
				batch := db.NewWriteBatch()
				for i := start; i < start+100 && i <= size; i++ {
					q := MessageUpdateMutation{Op: "update", ChannelID: "g", ChannelType: 2, Generation: "g", MessageID: uint64(i), MessageSeq: uint64(i), ExpectedChannelEpoch: 1, ExpectedRouteGeneration: 1, RequestID: "r", Digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Payload: []byte("payload")}
					if _, err = batch.ApplyMessageUpdate(0, q); err != nil {
						b.Fatal(err)
					}
				}
				if err = batch.Commit(); err != nil {
					b.Fatal(err)
				}
				batch.Close()
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				page, err := db.ForHashSlot(0).ReadMessageUpdates(ctx, MessageUpdateRead{ChannelID: "g", ChannelType: 2, After: uint64(size - 1), Limit: 100})
				if err != nil || len(page.Updates) != 1 || page.Next != uint64(size) {
					b.Fatalf("page=%+v err=%v", page, err)
				}
			}
		})
	}
}

// BenchmarkMessageUpdateAbsentExactIDs isolates the overlay's common case before
// the first edit. A pinned missing head proves all dependent edit tables empty.
func BenchmarkMessageUpdateAbsentExactIDs(b *testing.B) {
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	ids := make([]uint64, 200)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	for _, n := range []int{1, 3, 200} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			q := MessageUpdateRead{ChannelID: "unedited", ChannelType: 2, IDs: ids[:n]}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p, e := db.ForHashSlot(0).ReadMessageUpdates(context.Background(), q)
				if e != nil || len(p.Updates) != 0 {
					b.Fatalf("read: %v", e)
				}
			}
		})
	}
}

func TestMessageUpdateAbsentExactIDsHaveBoundedAllocations(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ids := make([]uint64, 200)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	allocations := func(n int) float64 {
		return testing.AllocsPerRun(30, func() {
			p, e := db.ForHashSlot(0).ReadMessageUpdates(context.Background(), MessageUpdateRead{ChannelID: "unedited", ChannelType: 2, IDs: ids[:n]})
			if e != nil || len(p.Updates) != 0 {
				t.Fatalf("read: %v", e)
			}
		})
	}
	one, many := allocations(1), allocations(200)
	// The allowance tolerates engine bookkeeping; it rejects work proportional
	// to all absent IDs rather than freezing a compiler-specific allocation count.
	if many > 2*one+10 {
		t.Fatalf("absent head must end exact lookup: one=%g, 200=%g allocations", one, many)
	}
}

func TestMessageUpdateEmptySnapshotProofDoesNotHideLaterEdit(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	q := MessageUpdateRead{ChannelID: "g", ChannelType: 2, IDs: []uint64{42}}
	check := func(want int) {
		t.Helper()
		p, e := db.ForHashSlot(0).ReadMessageUpdates(ctx, q)
		if e != nil || len(p.Updates) != want {
			t.Fatalf("updates=%d want=%d error=%v", len(p.Updates), want, e)
		}
	}
	check(0)
	wb := db.NewWriteBatch()
	defer wb.Close()
	if err = wb.UpsertChannel(0, Channel{ChannelID: "g", ChannelType: 2}); err != nil {
		t.Fatal(err)
	}
	if err = wb.UpsertChannelRuntimeMeta(0, ChannelRuntimeMeta{ChannelID: "g", ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, Leader: 1, MinISR: 1, Replicas: []uint64{1}, ISR: []uint64{1}}); err != nil {
		t.Fatal(err)
	}
	if _, err = wb.ApplyMessageUpdate(0, MessageUpdateMutation{Op: "init", ChannelID: "g", ChannelType: 2, Generation: "g"}); err != nil {
		t.Fatal(err)
	}
	if err = wb.Commit(); err != nil {
		t.Fatal(err)
	}
	check(0)
	edit := db.NewWriteBatch()
	defer edit.Close()
	if _, err = edit.ApplyMessageUpdate(0, MessageUpdateMutation{Op: "update", ChannelID: "g", ChannelType: 2, Generation: "g", MessageID: 42, MessageSeq: 1, ExpectedChannelEpoch: 1, ExpectedRouteGeneration: 1, RequestID: "r", Digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Payload: []byte("updated")}); err != nil {
		t.Fatal(err)
	}
	if err = edit.Commit(); err != nil {
		t.Fatal(err)
	}
	check(1)
	q.IncludePending = true
	check(1)
}
