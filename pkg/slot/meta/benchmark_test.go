package meta

import (
	"context"
	"fmt"
	"strconv"
	"testing"
)

type benchmarkFixtureConfig struct {
	Users        int
	ChannelIDs   int
	ChannelTypes int
}

type benchmarkSlotFixture struct {
	UserIDs    []string
	ChannelIDs []string
}

func TestBuildBenchmarkSlotFixtureCreatesQueryableData(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	fixture := buildBenchmarkSlotFixture(t, db, 9, benchmarkFixtureConfig{
		Users:        8,
		ChannelIDs:   3,
		ChannelTypes: 2,
	})

	if fixture.UserIDs[0] == "" || fixture.ChannelIDs[0] == "" {
		t.Fatalf("fixture = %#v", fixture)
	}
	if _, err := db.ForSlot(9).GetUser(ctx, fixture.UserIDs[0]); err != nil {
		t.Fatalf("GetUser(): %v", err)
	}
	list, err := db.ForSlot(9).ListChannelsByChannelID(ctx, fixture.ChannelIDs[0])
	if err != nil {
		t.Fatalf("ListChannelsByChannelID(): %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
}

func BenchmarkUserCreate(b *testing.B) {
	ctx := context.Background()
	db := openTestDB(b)
	shard := db.ForSlot(1)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := shard.CreateUser(ctx, User{
			UID:         benchmarkUserID(1, i),
			Token:       "token-" + strconv.Itoa(i),
			DeviceFlag:  int64(i % 4),
			DeviceLevel: int64(i % 8),
		})
		if err != nil {
			b.Fatalf("CreateUser(%d): %v", i, err)
		}
	}
}

func BenchmarkUserGet(b *testing.B) {
	ctx := context.Background()
	db := openTestDB(b)
	fixture := buildBenchmarkSlotFixture(b, db, 1, benchmarkFixtureConfig{
		Users:        128,
		ChannelIDs:   0,
		ChannelTypes: 0,
	})
	shard := db.ForSlot(1)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := shard.GetUser(ctx, fixture.UserIDs[i%len(fixture.UserIDs)]); err != nil {
			b.Fatalf("GetUser(%d): %v", i, err)
		}
	}
}

func BenchmarkUserUpdate(b *testing.B) {
	ctx := context.Background()
	db := openTestDB(b)
	fixture := buildBenchmarkSlotFixture(b, db, 1, benchmarkFixtureConfig{
		Users:        64,
		ChannelIDs:   0,
		ChannelTypes: 0,
	})
	shard := db.ForSlot(1)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		uid := fixture.UserIDs[i%len(fixture.UserIDs)]
		err := shard.UpdateUser(ctx, User{
			UID:         uid,
			Token:       "updated-" + strconv.Itoa(i),
			DeviceFlag:  int64(i % 16),
			DeviceLevel: int64((i / 2) % 16),
		})
		if err != nil {
			b.Fatalf("UpdateUser(%d): %v", i, err)
		}
	}
}

func BenchmarkChannelCreateAndList(b *testing.B) {
	b.Run("create", func(b *testing.B) {
		ctx := context.Background()
		db := openTestDB(b)
		shard := db.ForSlot(1)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			err := shard.CreateChannel(ctx, Channel{
				ChannelID:   benchmarkChannelID(1, i),
				ChannelType: int64((i % 4) + 1),
				Ban:         int64(i % 2),
			})
			if err != nil {
				b.Fatalf("CreateChannel(%d): %v", i, err)
			}
		}
	})

	b.Run("list", func(b *testing.B) {
		ctx := context.Background()
		db := openTestDB(b)
		fixture := buildBenchmarkSlotFixture(b, db, 1, benchmarkFixtureConfig{
			Users:        0,
			ChannelIDs:   16,
			ChannelTypes: 4,
		})
		shard := db.ForSlot(1)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := shard.ListChannelsByChannelID(ctx, fixture.ChannelIDs[i%len(fixture.ChannelIDs)]); err != nil {
				b.Fatalf("ListChannelsByChannelID(%d): %v", i, err)
			}
		}
	})
}

func BenchmarkDeleteSlotData(b *testing.B) {
	ctx := context.Background()
	db := openTestDB(b)
	slot := uint64(1)
	cfg := benchmarkFixtureConfig{
		Users:        8,
		ChannelIDs:   4,
		ChannelTypes: 2,
	}
	buildBenchmarkSlotFixture(b, db, slot, cfg)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.DeleteSlotData(ctx, slot); err != nil {
			b.Fatalf("DeleteSlotData(%d): %v", i, err)
		}

		b.StopTimer()
		buildBenchmarkSlotFixture(b, db, slot, cfg)
		b.StartTimer()
	}
}

func BenchmarkSlotSnapshotExportImport(b *testing.B) {
	b.Run("export", func(b *testing.B) {
		ctx := context.Background()
		db := openTestDB(b)
		slot := uint64(7)
		buildBenchmarkSlotFixture(b, db, slot, benchmarkFixtureConfig{
			Users:        32,
			ChannelIDs:   8,
			ChannelTypes: 4,
		})

		snapshotBytes := 0
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			snap, err := db.ExportSlotSnapshot(ctx, slot)
			if err != nil {
				b.Fatalf("ExportSlotSnapshot(%d): %v", i, err)
			}
			snapshotBytes = len(snap.Data)
		}
		b.StopTimer()
		b.ReportMetric(float64(snapshotBytes), "snap_bytes")
	})

	b.Run("import", func(b *testing.B) {
		ctx := context.Background()
		source := openTestDB(b)
		slot := uint64(7)
		buildBenchmarkSlotFixture(b, source, slot, benchmarkFixtureConfig{
			Users:        32,
			ChannelIDs:   8,
			ChannelTypes: 4,
		})
		snap, err := source.ExportSlotSnapshot(ctx, slot)
		if err != nil {
			b.Fatalf("ExportSlotSnapshot(): %v", err)
		}

		target := openTestDB(b)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := target.ImportSlotSnapshot(ctx, snap); err != nil {
				b.Fatalf("ImportSlotSnapshot(%d): %v", i, err)
			}
		}
		b.StopTimer()
		b.ReportMetric(float64(len(snap.Data)), "snap_bytes")
	})
}

func buildBenchmarkSlotFixture(tb testing.TB, db *DB, slot uint64, cfg benchmarkFixtureConfig) benchmarkSlotFixture {
	tb.Helper()

	ctx := context.Background()
	shard := db.ForSlot(slot)
	fixture := benchmarkSlotFixture{
		UserIDs:    make([]string, 0, cfg.Users),
		ChannelIDs: make([]string, 0, cfg.ChannelIDs),
	}

	for i := 0; i < cfg.Users; i++ {
		uid := benchmarkUserID(slot, i)
		err := shard.CreateUser(ctx, User{
			UID:         uid,
			Token:       "token-" + strconv.Itoa(i),
			DeviceFlag:  int64(i % 4),
			DeviceLevel: int64(i % 8),
		})
		if err != nil {
			tb.Fatalf("CreateUser(slot=%d, uid=%q): %v", slot, uid, err)
		}
		fixture.UserIDs = append(fixture.UserIDs, uid)
	}

	for i := 0; i < cfg.ChannelIDs; i++ {
		channelID := benchmarkChannelID(slot, i)
		fixture.ChannelIDs = append(fixture.ChannelIDs, channelID)
		for j := 0; j < cfg.ChannelTypes; j++ {
			channelType := int64(j + 1)
			err := shard.CreateChannel(ctx, Channel{
				ChannelID:   channelID,
				ChannelType: channelType,
				Ban:         int64((i + j) % 2),
			})
			if err != nil {
				tb.Fatalf("CreateChannel(slot=%d, channelID=%q, type=%d): %v", slot, channelID, channelType, err)
			}
		}
	}

	return fixture
}

func benchmarkUserID(slot uint64, index int) string {
	return fmt.Sprintf("bench-user-%d-%d", slot, index)
}

func benchmarkChannelID(slot uint64, index int) string {
	return fmt.Sprintf("bench-channel-%d-%d", slot, index)
}
