package meta

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestChannelRuntimeMetaUpsertGetNormalizeAndDelete(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(7)

	meta := testRuntimeMeta("runtime-a", 2)
	meta.Replicas = []uint64{3, 1, 2, 2}
	meta.ISR = []uint64{3, 1, 3}
	result, err := shard.UpsertChannelRuntimeMeta(context.Background(), meta)
	if err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(): %v", err)
	}
	if result != MonotonicApplied {
		t.Fatalf("upsert result = %v, want applied", result)
	}
	got, ok, err := shard.GetChannelRuntimeMeta(context.Background(), meta.ChannelID, meta.ChannelType)
	if err != nil || !ok {
		t.Fatalf("GetChannelRuntimeMeta() ok=%v err=%v", ok, err)
	}
	want := normalizeChannelRuntimeMeta(meta)
	if !equalRuntimeMeta(got, want) {
		t.Fatalf("runtime meta = %+v, want %+v", got, want)
	}
	if err := shard.DeleteChannelRuntimeMeta(context.Background(), meta.ChannelID, meta.ChannelType); err != nil {
		t.Fatalf("DeleteChannelRuntimeMeta(): %v", err)
	}
	if _, ok, err := shard.GetChannelRuntimeMeta(context.Background(), meta.ChannelID, meta.ChannelType); err != nil || ok {
		t.Fatalf("GetChannelRuntimeMeta(deleted) ok=%v err=%v, want missing", ok, err)
	}
}

func TestChannelRuntimeMetaKeepsLegacyRowLayoutAndKeyBoundValue(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(7)

	meta := testRuntimeMeta("runtime-layout", 2)
	if _, err := shard.UpsertChannelRuntimeMeta(context.Background(), meta); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(): %v", err)
	}

	legacyKey := encodeChannelRuntimeMetaRowKey(7, meta.ChannelID, meta.ChannelType, channelRuntimeMetaPrimaryFamilyID)
	runtimeKey, err := channelRuntimeMetaTable.primaryRowKey(7, channelRuntimeMetaPrimaryKey(meta.ChannelID, meta.ChannelType))
	if err != nil {
		t.Fatalf("runtime primary row key: %v", err)
	}
	if string(runtimeKey) != string(legacyKey) {
		t.Fatalf("runtime row key %x, want legacy %x", runtimeKey, legacyKey)
	}
	stored, ok, err := store.db.get(legacyKey)
	if err != nil || !ok {
		t.Fatalf("legacy row ok=%v err=%v", ok, err)
	}
	got, err := channelRuntimeMetaTable.decodeValue(legacyKey, channelRuntimeMetaPrimaryKey(meta.ChannelID, meta.ChannelType), stored)
	if err != nil {
		t.Fatalf("decode runtime value: %v", err)
	}
	if !equalRuntimeMeta(got, normalizeChannelRuntimeMeta(meta)) {
		t.Fatalf("decoded meta = %+v, want %+v", got, normalizeChannelRuntimeMeta(meta))
	}
	wrongKey := append(append([]byte(nil), legacyKey...), 0xff)
	if _, err := channelRuntimeMetaTable.decodeValue(wrongKey, channelRuntimeMetaPrimaryKey(meta.ChannelID, meta.ChannelType), stored); err == nil {
		t.Fatal("decode with wrong row key err = nil, want checksum failure")
	}
}

func TestChannelRuntimeMetaMonotonicAppliedIgnoredStaleAndConflict(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(7)

	base := testRuntimeMeta("runtime-monotonic", 1)
	base.ChannelEpoch = 10
	base.LeaderEpoch = 20
	base.Leader = 1
	base.LeaseUntilMS = 3000
	if result, err := shard.UpsertChannelRuntimeMeta(context.Background(), base); err != nil || result != MonotonicApplied {
		t.Fatalf("base upsert = %v err %v, want applied", result, err)
	}

	stale := base
	stale.LeaderEpoch = 19
	result, err := shard.UpsertChannelRuntimeMeta(context.Background(), stale)
	if err != nil {
		t.Fatalf("stale upsert err = %v", err)
	}
	if result != MonotonicIgnoredStale {
		t.Fatalf("stale upsert result = %v, want ignored stale", result)
	}

	conflict := base
	conflict.Leader = 2
	conflict.ISR = []uint64{1, 2}
	result, err = shard.UpsertChannelRuntimeMeta(context.Background(), conflict)
	if !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("conflict upsert err = %v, want conflict", err)
	}
	if result != MonotonicConflict {
		t.Fatalf("conflict upsert result = %v, want conflict", result)
	}

	advanced := base
	advanced.LeaderEpoch = 21
	advanced.LeaseUntilMS = 5000
	result, err = shard.UpsertChannelRuntimeMeta(context.Background(), advanced)
	if err != nil || result != MonotonicApplied {
		t.Fatalf("advanced upsert = %v err %v, want applied", result, err)
	}
	got, ok, err := shard.GetChannelRuntimeMeta(context.Background(), base.ChannelID, base.ChannelType)
	if err != nil || !ok {
		t.Fatalf("GetChannelRuntimeMeta() ok=%v err=%v", ok, err)
	}
	if got.LeaderEpoch != 21 || got.RouteGeneration <= normalizeChannelRuntimeMeta(base).RouteGeneration {
		t.Fatalf("advanced meta = %+v, want leader epoch 21 and route generation bump", got)
	}
}

func TestChannelRuntimeMetaAdvanceRetention(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(7)
	base := testRuntimeMeta("runtime-retention", 2)
	base.ChannelEpoch = 10
	base.LeaderEpoch = 20
	base.Leader = 1
	base.LeaseUntilMS = 3000
	base.RetentionThroughSeq = 30
	base.RetentionUpdatedAtMS = 3500
	if _, err := shard.UpsertChannelRuntimeMeta(context.Background(), base); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(): %v", err)
	}

	err := shard.AdvanceChannelRetentionThroughSeq(context.Background(), ChannelRetentionAdvance{
		ChannelID:            base.ChannelID,
		ChannelType:          base.ChannelType,
		ExpectedChannelEpoch: base.ChannelEpoch,
		ExpectedLeaderEpoch:  base.LeaderEpoch,
		ExpectedLeader:       base.Leader,
		ExpectedLeaseUntilMS: base.LeaseUntilMS,
		RetentionThroughSeq:  42,
		RetentionUpdatedAtMS: 4000,
	})
	if err != nil {
		t.Fatalf("AdvanceChannelRetentionThroughSeq(): %v", err)
	}
	got, ok, err := shard.GetChannelRuntimeMeta(context.Background(), base.ChannelID, base.ChannelType)
	if err != nil || !ok {
		t.Fatalf("GetChannelRuntimeMeta() ok=%v err=%v", ok, err)
	}
	if got.RetentionThroughSeq != 42 || got.RetentionUpdatedAtMS != 4000 {
		t.Fatalf("retention = (%d,%d), want (42,4000)", got.RetentionThroughSeq, got.RetentionUpdatedAtMS)
	}

	err = shard.AdvanceChannelRetentionThroughSeq(context.Background(), ChannelRetentionAdvance{
		ChannelID:            base.ChannelID,
		ChannelType:          base.ChannelType,
		ExpectedChannelEpoch: base.ChannelEpoch + 1,
		ExpectedLeaderEpoch:  base.LeaderEpoch,
		ExpectedLeader:       base.Leader,
		ExpectedLeaseUntilMS: base.LeaseUntilMS,
		RetentionThroughSeq:  50,
		RetentionUpdatedAtMS: 5000,
	})
	if !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("stale retention err = %v, want conflict", err)
	}
}

func testRuntimeMeta(channelID string, channelType int64) ChannelRuntimeMeta {
	return ChannelRuntimeMeta{
		ChannelID:    channelID,
		ChannelType:  channelType,
		ChannelEpoch: 3,
		LeaderEpoch:  2,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2},
		Leader:       1,
		MinISR:       2,
		Status:       2,
		Features:     1,
		LeaseUntilMS: 1700000000000,
	}
}

func equalRuntimeMeta(a, b ChannelRuntimeMeta) bool {
	if a.ChannelID != b.ChannelID || a.ChannelType != b.ChannelType || a.ChannelEpoch != b.ChannelEpoch ||
		a.LeaderEpoch != b.LeaderEpoch || a.RouteGeneration != b.RouteGeneration || a.Leader != b.Leader ||
		a.MinISR != b.MinISR || a.Status != b.Status || a.Features != b.Features || a.LeaseUntilMS != b.LeaseUntilMS ||
		a.RetentionThroughSeq != b.RetentionThroughSeq || a.RetentionUpdatedAtMS != b.RetentionUpdatedAtMS ||
		a.WriteFenceToken != b.WriteFenceToken || a.WriteFenceVersion != b.WriteFenceVersion ||
		a.WriteFenceReason != b.WriteFenceReason || a.WriteFenceUntilMS != b.WriteFenceUntilMS {
		return false
	}
	if len(a.Replicas) != len(b.Replicas) || len(a.ISR) != len(b.ISR) {
		return false
	}
	for i := range a.Replicas {
		if a.Replicas[i] != b.Replicas[i] {
			return false
		}
	}
	for i := range a.ISR {
		if a.ISR[i] != b.ISR[i] {
			return false
		}
	}
	return true
}
