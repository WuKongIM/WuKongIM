package meta

import (
	"context"
	"errors"
	"io"
	"math"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
)

func TestSubscriberIncarnationStableAndAtomic(t *testing.T) {
	db := openMembershipContractDB(t)
	ctx := context.Background()
	s := db.MetaDB().HashSlot(7)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.CreateChannel(ctx, Channel{ChannelID: "g", ChannelType: 2}))
	read := func(uid string) uint64 {
		t.Helper()
		r, ok, err := s.GetSubscriber(ctx, "g", 2, uid)
		if err != nil || !ok || r.Incarnation < 2 {
			t.Fatalf("member=%+v found=%v err=%v", r, ok, err)
		}
		return r.Incarnation
	}
	must(s.AddSubscribers(ctx, "g", 2, []string{"b", "a", "a"}, 7))
	first := read("a")
	if read("b") != first+1 {
		t.Fatal("allocation not canonical")
	}
	must(s.AddSubscribers(ctx, "g", 2, []string{"a", "c"}, 7))
	if read("a") != first {
		t.Fatal("duplicate or unrelated add changed incarnation")
	}
	must(s.RemoveSubscribers(ctx, "g", 2, []string{"a"}, 7))
	must(s.AddSubscribers(ctx, "g", 2, []string{"a"}, 7))
	if read("a") <= first {
		t.Fatal("same-version rejoin restored incarnation")
	}
	previous, err := s.SubscriberSequence(ctx)
	must(err)
	b := db.NewWriteBatch()
	defer b.Close()
	must(b.AddSubscribers(7, "g", 2, []string{"new"}, 7))
	must(b.RemoveSubscribers(7, "g", 2, []string{"a"}, 7))
	must(b.AddSubscribers(7, "g", 2, []string{"a"}, 7))
	must(b.DeleteChannel(7, "g", 2))
	must(b.UpsertChannel(7, Channel{ChannelID: "g", ChannelType: 2}))
	must(b.AddSubscribers(7, "g", 2, []string{"a", "new", "b"}, 7))
	must(b.Commit())
	if read("a") <= previous+2 || read("b") <= previous+2 || read("new") <= previous+2 {
		t.Fatal("delete/recreate reused staged or disk identity")
	}
	ch, ok, err := s.GetChannel(ctx, "g", 2)
	must(err)
	if !ok || ch.SubscriberCount != 3 {
		t.Fatalf("channel=%+v", ch)
	}
	seq, err := s.SubscriberSequence(ctx)
	must(err)
	failed := db.NewWriteBatch()
	defer failed.Close()
	must(failed.AddSubscribers(7, "g", 2, []string{"rollback"}, 8))
	must(failed.AddSubscribers(7, "g", 2, []string{"stale"}, 6))
	if err = failed.Commit(); !errors.Is(err, ErrStaleMeta) {
		t.Fatalf("late failure=%v", err)
	}
	after, err := s.SubscriberSequence(ctx)
	must(err)
	if after != seq {
		t.Fatal("failed batch consumed sequence")
	}
	if ok, err := s.ContainsSubscriber(ctx, "g", 2, "rollback"); err != nil || ok {
		t.Fatal("failed batch leaked member")
	}
}

func TestSubscriberIncarnationLegacyAndCorruption(t *testing.T) {
	st := openTestMetaStore(t)
	defer st.close(t)
	s, ctx := st.db.HashSlot(4), context.Background()
	if err := s.CreateChannel(ctx, Channel{ChannelID: "g", ChannelType: 2}); err != nil {
		t.Fatal(err)
	}
	key, _ := subscriberRowKey(4, "g", 2, "u")
	write := func(k, v []byte) {
		t.Helper()
		b := st.engine.NewBatch()
		defer b.Close()
		if err := b.Set(k, v); err != nil {
			t.Fatal(err)
		}
		if err := b.Commit(true); err != nil {
			t.Fatal(err)
		}
	}
	write(key, nil)
	r, ok, err := s.GetSubscriber(ctx, "g", 2, "u")
	if err != nil || !ok || r.Incarnation != 1 {
		t.Fatalf("legacy=%+v %v %v", r, ok, err)
	}
	if err = s.AddSubscribers(ctx, "g", 2, []string{"u"}, 0); err != nil {
		t.Fatal(err)
	}
	r, _, err = s.GetSubscriber(ctx, "g", 2, "u")
	if err != nil || r.Incarnation != 1 {
		t.Fatal("legacy duplicate changed identity")
	}
	var w rowcodec.Writer
	if err = w.Uint64(4, 37); err != nil {
		t.Fatal(err)
	}
	if err = w.String(5, "future"); err != nil {
		t.Fatal(err)
	}
	value := rowcodec.Wrap(key, 1, rowcodec.CodecColumns, rowcodec.FlagChecksum, w.Bytes())
	write(key, value)
	r, _, err = s.GetSubscriber(ctx, "g", 2, "u")
	if err != nil || r.Incarnation != 37 {
		t.Fatalf("future=%+v %v", r, err)
	}
	for _, bad := range [][]byte{{1}, value[:len(value)-1], rowcodec.Wrap([]byte("other"), 1, rowcodec.CodecColumns, rowcodec.FlagChecksum, w.Bytes())} {
		write(key, bad)
		if _, _, err = s.GetSubscriber(ctx, "g", 2, "u"); err == nil {
			t.Fatal("corrupt member accepted")
		}
	}
	write(key, nil)
	if err = s.ImportSubscriberSequence(ctx, math.MaxUint64); err != nil {
		t.Fatal(err)
	}
	if err = s.AddSubscribers(ctx, "g", 2, []string{"fresh"}, 0); !errors.Is(err, ErrStaleMeta) {
		t.Fatalf("exhaustion=%v", err)
	}
	if ok, err := s.ContainsSubscriber(ctx, "g", 2, "fresh"); err != nil || ok {
		t.Fatal("exhaustion leaked member")
	}
	write(subscriberSequenceKey(4), []byte{1})
	if err = s.AddSubscribers(ctx, "g", 2, []string{"fresh"}, 0); err == nil {
		t.Fatal("corrupt allocator accepted")
	}
}

func TestSubscriberIncarnationSnapshotsPreserveDeletedHighWater(t *testing.T) {
	for _, backup := range []bool{false, true} {
		t.Run(map[bool]string{false: "raft", true: "backup"}[backup], func(t *testing.T) {
			st := openTestMetaStore(t)
			defer st.close(t)
			ctx := context.Background()
			s := st.db.HashSlot(5)
			if err := s.CreateChannel(ctx, Channel{ChannelID: "g", ChannelType: 2}); err != nil {
				t.Fatal(err)
			}
			if err := s.AddSubscribers(ctx, "g", 2, []string{"u"}, 0); err != nil {
				t.Fatal(err)
			}
			before, _, err := s.GetSubscriber(ctx, "g", 2, "u")
			if err != nil {
				t.Fatal(err)
			}
			if err = s.DeleteChannel(ctx, "g", 2); err != nil {
				t.Fatal(err)
			}
			var reader io.ReadCloser
			if backup {
				reader, err = st.db.OpenBackupHashSlotSnapshot(ctx, []uint16{5})
			} else {
				reader, err = st.db.OpenHashSlotSnapshot(ctx, []uint16{5})
			}
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			body, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			target := openTestMetaStore(t)
			defer target.close(t)
			if err = target.db.ImportHashSlotSnapshot(ctx, SlotSnapshot{HashSlots: []uint16{5}, Data: body}); err != nil {
				t.Fatal(err)
			}
			ns := target.db.HashSlot(5)
			if err = ns.CreateChannel(ctx, Channel{ChannelID: "g", ChannelType: 2}); err != nil {
				t.Fatal(err)
			}
			if err = ns.AddSubscribers(ctx, "g", 2, []string{"u"}, 0); err != nil {
				t.Fatal(err)
			}
			after, _, err := ns.GetSubscriber(ctx, "g", 2, "u")
			if err != nil || after.Incarnation <= before.Incarnation {
				t.Fatalf("reused restored identity %+v err=%v", after, err)
			}
		})
	}
}

func TestSubscriberIncarnationImportExactAndBounded(t *testing.T) {
	st := openTestMetaStore(t)
	defer st.close(t)
	ctx := context.Background()
	s := st.db.HashSlot(6)
	if err := s.CreateChannel(ctx, Channel{ChannelID: "g", ChannelType: 2}); err != nil {
		t.Fatal(err)
	}
	rows := []Subscriber{{ChannelID: "g", ChannelType: 2, UID: "a", Incarnation: 9007199254740993}, {ChannelID: "g", ChannelType: 2, UID: "legacy"}}
	if err := s.ImportSubscriberSequence(ctx, 9007199254741000); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.ImportSubscribers(ctx, "g", 2, rows); err != nil {
			t.Fatal(err)
		}
	}
	a, _, err := s.GetSubscriber(ctx, "g", 2, "a")
	if err != nil || a.Incarnation != rows[0].Incarnation {
		t.Fatalf("exact=%+v %v", a, err)
	}
	rows[0].Incarnation++
	if err = s.ImportSubscribers(ctx, "g", 2, rows); !errors.Is(err, ErrStaleMeta) {
		t.Fatalf("conflicting import=%v", err)
	}
	if err = s.ImportSubscriberSequence(ctx, 2); !errors.Is(err, ErrStaleMeta) {
		t.Fatalf("regressed sequence=%v", err)
	}
	if err = s.AddSubscribers(ctx, "g", 2, []string{"new"}, 0); err != nil {
		t.Fatal(err)
	}
	r, _, err := s.GetSubscriber(ctx, "g", 2, "new")
	if err != nil || r.Incarnation != 9007199254741001 {
		t.Fatalf("next=%+v %v", r, err)
	}
	ch, _, err := s.GetChannel(ctx, "g", 2)
	if err != nil || ch.SubscriberCount != 3 {
		t.Fatalf("count=%+v %v", ch, err)
	}
}
