package meta

import (
	"context"
	"fmt"
	"strconv"
	"testing"
)

type benchmarkMetaFixtureConfig struct {
	Users          int
	ChannelIDs     int
	ChannelTypes   int
	Subscribers    int
	RuntimeMetas   int
	PluginBindings int
}

type benchmarkMetaFixture struct {
	UserIDs    []string
	ChannelIDs []string
}

func BenchmarkUserCreate(b *testing.B) {
	ctx := context.Background()
	store := openTestMetaStore(b)
	defer store.close(b)
	shard := store.db.HashSlot(1)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := shard.CreateUser(ctx, User{
			UID:         benchmarkUserID(1, i),
			Token:       "token-" + strconv.Itoa(i),
			DeviceFlag:  int64(i % 4),
			DeviceLevel: int64(i % 8),
		}); err != nil {
			b.Fatalf("CreateUser(%d): %v", i, err)
		}
	}
}

func BenchmarkUserGet(b *testing.B) {
	ctx := context.Background()
	store := openTestMetaStore(b)
	defer store.close(b)
	fixture := buildBenchmarkMetaFixture(b, store.db, 1, benchmarkMetaFixtureConfig{
		Users: 128,
	})
	shard := store.db.HashSlot(1)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok, err := shard.GetUser(ctx, fixture.UserIDs[i%len(fixture.UserIDs)]); err != nil || !ok {
			b.Fatalf("GetUser(%d) ok=%v err=%v", i, ok, err)
		}
	}
}

func BenchmarkUserUpdate(b *testing.B) {
	ctx := context.Background()
	store := openTestMetaStore(b)
	defer store.close(b)
	fixture := buildBenchmarkMetaFixture(b, store.db, 1, benchmarkMetaFixtureConfig{
		Users: 64,
	})
	shard := store.db.HashSlot(1)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		uid := fixture.UserIDs[i%len(fixture.UserIDs)]
		if err := shard.UpdateUser(ctx, User{
			UID:         uid,
			Token:       "updated-" + strconv.Itoa(i),
			DeviceFlag:  int64(i % 16),
			DeviceLevel: int64((i / 2) % 16),
		}); err != nil {
			b.Fatalf("UpdateUser(%d): %v", i, err)
		}
	}
}

func BenchmarkChannelCachedGet(b *testing.B) {
	ctx := context.Background()
	store := openTestMetaStore(b)
	defer store.close(b)
	fixture := buildBenchmarkMetaFixture(b, store.db, 1, benchmarkMetaFixtureConfig{
		ChannelIDs:   64,
		ChannelTypes: 1,
	})
	shard := store.db.HashSlot(1)
	for _, channelID := range fixture.ChannelIDs {
		if _, ok, err := shard.GetChannel(ctx, channelID, 1); err != nil || !ok {
			b.Fatalf("warm GetChannel(%q) ok=%v err=%v", channelID, ok, err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		channelID := fixture.ChannelIDs[i%len(fixture.ChannelIDs)]
		if _, ok, err := shard.GetChannel(ctx, channelID, 1); err != nil || !ok {
			b.Fatalf("GetChannel(%d) ok=%v err=%v", i, ok, err)
		}
	}
}

func BenchmarkSubscriberAddPage(b *testing.B) {
	b.Run("add", func(b *testing.B) {
		ctx := context.Background()
		store := openTestMetaStore(b)
		defer store.close(b)
		shard := store.db.HashSlot(1)
		if err := shard.CreateChannel(ctx, Channel{ChannelID: "bench-subs", ChannelType: 1}); err != nil {
			b.Fatalf("CreateChannel(): %v", err)
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := shard.AddSubscribers(ctx, "bench-subs", 1, []string{benchmarkUserID(1, i)}, uint64(i+1)); err != nil {
				b.Fatalf("AddSubscribers(%d): %v", i, err)
			}
		}
	})

	b.Run("page", func(b *testing.B) {
		ctx := context.Background()
		store := openTestMetaStore(b)
		defer store.close(b)
		buildBenchmarkMetaFixture(b, store.db, 1, benchmarkMetaFixtureConfig{
			ChannelIDs:   1,
			ChannelTypes: 1,
			Subscribers:  512,
		})
		shard := store.db.HashSlot(1)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, _, _, err := shard.ListSubscribersPage(ctx, benchmarkChannelID(1, 0), 1, "", 64); err != nil {
				b.Fatalf("ListSubscribersPage(%d): %v", i, err)
			}
		}
	})
}

func BenchmarkRuntimeMetaUpsert(b *testing.B) {
	ctx := context.Background()
	store := openTestMetaStore(b)
	defer store.close(b)
	shard := store.db.HashSlot(1)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := shard.UpsertChannelRuntimeMeta(ctx, benchmarkRuntimeMeta(1, i%32, uint64(i/32+1))); err != nil {
			b.Fatalf("UpsertChannelRuntimeMeta(%d): %v", i, err)
		}
	}
}

func BenchmarkMixedBatch(b *testing.B) {
	ctx := context.Background()
	store := openTestMetaStore(b)
	defer store.close(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batch := store.db.NewBatch()
		if err := batch.CreateUser(1, User{UID: benchmarkUserID(1, i), Token: "token-" + strconv.Itoa(i)}); err != nil {
			b.Fatalf("CreateUser(%d): %v", i, err)
		}
		if err := batch.UpsertChannel(1, Channel{ChannelID: benchmarkChannelID(1, i), ChannelType: 1, Ban: int64(i % 2)}); err != nil {
			b.Fatalf("UpsertChannel(%d): %v", i, err)
		}
		if _, err := batch.UpsertChannelRuntimeMeta(1, benchmarkRuntimeMeta(1, i, uint64(i+1))); err != nil {
			b.Fatalf("UpsertChannelRuntimeMeta(%d): %v", i, err)
		}
		if err := batch.Commit(ctx); err != nil {
			b.Fatalf("Commit(%d): %v", i, err)
		}
	}
}

func BenchmarkHashSlotSnapshotExportImport(b *testing.B) {
	b.Run("export", func(b *testing.B) {
		ctx := context.Background()
		store := openTestMetaStore(b)
		defer store.close(b)
		buildBenchmarkMetaFixture(b, store.db, 7, benchmarkMetaFixtureConfig{
			Users:          32,
			ChannelIDs:     8,
			ChannelTypes:   2,
			Subscribers:    64,
			RuntimeMetas:   16,
			PluginBindings: 16,
		})

		snapshotBytes := 0
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			snap, err := store.db.ExportHashSlotSnapshot(ctx, []uint16{7})
			if err != nil {
				b.Fatalf("ExportHashSlotSnapshot(%d): %v", i, err)
			}
			snapshotBytes = len(snap.Data)
		}
		b.StopTimer()
		b.ReportMetric(float64(snapshotBytes), "snap_bytes")
	})

	b.Run("import", func(b *testing.B) {
		ctx := context.Background()
		source := openTestMetaStore(b)
		defer source.close(b)
		buildBenchmarkMetaFixture(b, source.db, 7, benchmarkMetaFixtureConfig{
			Users:          32,
			ChannelIDs:     8,
			ChannelTypes:   2,
			Subscribers:    64,
			RuntimeMetas:   16,
			PluginBindings: 16,
		})
		snap, err := source.db.ExportHashSlotSnapshot(ctx, []uint16{7})
		if err != nil {
			b.Fatalf("ExportHashSlotSnapshot(): %v", err)
		}

		target := openTestMetaStore(b)
		defer target.close(b)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := target.db.ImportHashSlotSnapshot(ctx, snap); err != nil {
				b.Fatalf("ImportHashSlotSnapshot(%d): %v", i, err)
			}
		}
		b.StopTimer()
		b.ReportMetric(float64(len(snap.Data)), "snap_bytes")
	})
}

func buildBenchmarkMetaFixture(tb testing.TB, db *MetaDB, hashSlot HashSlot, cfg benchmarkMetaFixtureConfig) benchmarkMetaFixture {
	tb.Helper()

	ctx := context.Background()
	shard := db.HashSlot(hashSlot)
	fixture := benchmarkMetaFixture{
		UserIDs:    make([]string, 0, cfg.Users),
		ChannelIDs: make([]string, 0, cfg.ChannelIDs),
	}

	for i := 0; i < cfg.Users; i++ {
		uid := benchmarkUserID(hashSlot, i)
		if err := shard.CreateUser(ctx, User{
			UID:         uid,
			Token:       "token-" + strconv.Itoa(i),
			DeviceFlag:  int64(i % 4),
			DeviceLevel: int64(i % 8),
		}); err != nil {
			tb.Fatalf("CreateUser(hashSlot=%d, uid=%q): %v", hashSlot, uid, err)
		}
		fixture.UserIDs = append(fixture.UserIDs, uid)
	}

	for i := 0; i < cfg.ChannelIDs; i++ {
		channelID := benchmarkChannelID(hashSlot, i)
		fixture.ChannelIDs = append(fixture.ChannelIDs, channelID)
		for j := 0; j < cfg.ChannelTypes; j++ {
			channelType := int64(j + 1)
			if err := shard.CreateChannel(ctx, Channel{
				ChannelID:   channelID,
				ChannelType: channelType,
				Ban:         int64((i + j) % 2),
			}); err != nil {
				tb.Fatalf("CreateChannel(hashSlot=%d, channelID=%q, type=%d): %v", hashSlot, channelID, channelType, err)
			}
		}
	}

	if cfg.Subscribers > 0 && cfg.ChannelIDs > 0 && cfg.ChannelTypes > 0 {
		uids := make([]string, 0, cfg.Subscribers)
		for i := 0; i < cfg.Subscribers; i++ {
			uids = append(uids, benchmarkSubscriberID(hashSlot, i))
		}
		if err := shard.AddSubscribers(ctx, fixture.ChannelIDs[0], 1, uids, uint64(cfg.Subscribers)); err != nil {
			tb.Fatalf("AddSubscribers(hashSlot=%d): %v", hashSlot, err)
		}
	}

	for i := 0; i < cfg.RuntimeMetas; i++ {
		if _, err := shard.UpsertChannelRuntimeMeta(ctx, benchmarkRuntimeMeta(hashSlot, i, 1)); err != nil {
			tb.Fatalf("UpsertChannelRuntimeMeta(hashSlot=%d, index=%d): %v", hashSlot, i, err)
		}
	}

	for i := 0; i < cfg.PluginBindings; i++ {
		if err := shard.BindPluginUser(ctx, PluginUserBinding{
			UID:         benchmarkSubscriberID(hashSlot, i),
			PluginNo:    "plugin-" + strconv.Itoa(i%4),
			CreatedAtMS: int64(i + 1),
			UpdatedAtMS: int64(i + 1),
		}); err != nil {
			tb.Fatalf("BindPluginUser(hashSlot=%d, index=%d): %v", hashSlot, i, err)
		}
	}

	return fixture
}

func benchmarkRuntimeMeta(hashSlot HashSlot, index int, epoch uint64) ChannelRuntimeMeta {
	return ChannelRuntimeMeta{
		ChannelID:    benchmarkRuntimeChannelID(hashSlot, index),
		ChannelType:  1,
		ChannelEpoch: epoch,
		LeaderEpoch:  epoch,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       1,
		MinISR:       2,
		Status:       1,
		LeaseUntilMS: int64(epoch),
	}
}

func benchmarkUserID(hashSlot HashSlot, index int) string {
	return fmt.Sprintf("bench-user-%d-%d", hashSlot, index)
}

func benchmarkChannelID(hashSlot HashSlot, index int) string {
	return fmt.Sprintf("bench-channel-%d-%d", hashSlot, index)
}

func benchmarkRuntimeChannelID(hashSlot HashSlot, index int) string {
	return fmt.Sprintf("bench-runtime-%d-%d", hashSlot, index)
}

func benchmarkSubscriberID(hashSlot HashSlot, index int) string {
	return fmt.Sprintf("bench-subscriber-%d-%d", hashSlot, index)
}
