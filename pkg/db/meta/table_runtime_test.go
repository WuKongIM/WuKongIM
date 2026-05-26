package meta

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

type runtimeTestRow struct {
	ID    string
	Owner string
	Value string
}

func newRuntimeTestTable(t *testing.T) Table[runtimeTestRow] {
	t.Helper()
	table, err := registerMetaTableInRegistry(newMetaTableRegistry(), TableSpec[runtimeTestRow]{
		ID:   65030,
		Name: "runtime_test",
		Columns: []schema.Column{
			{ID: 1, Name: "id", Type: schema.TypeString, Required: true},
			{ID: 2, Name: "owner", Type: schema.TypeString},
			{ID: 3, Name: "value", Type: schema.TypeString},
		},
		Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{2, 3}}},
		Primary: PrimarySpec[runtimeTestRow]{
			IndexID:  1,
			FamilyID: 0,
			Name:     "pk_runtime_test",
			Columns:  []uint16{1},
			Layout:   KeyLayout{KeyString},
			Key:      func(row runtimeTestRow) KeyParts { return KeyParts{String(row.ID)} },
		},
		Indexes: []IndexSpec[runtimeTestRow]{
			{
				ID:      2,
				Name:    "idx_runtime_test_owner",
				Columns: []uint16{2, 1},
				Layout:  KeyLayout{KeyString, KeyString},
				Key: func(row runtimeTestRow) (KeyParts, bool) {
					if row.Owner == "" {
						return nil, false
					}
					return KeyParts{String(row.Owner), String(row.ID)}, true
				},
			},
			{
				ID:      3,
				Name:    "uidx_runtime_test_value",
				Unique:  true,
				Columns: []uint16{3},
				Layout:  KeyLayout{KeyString},
				Key: func(row runtimeTestRow) (KeyParts, bool) {
					if row.Value == "" {
						return nil, false
					}
					return KeyParts{String(row.Value)}, true
				},
			},
		},
		Validate: func(row runtimeTestRow) error {
			return validateKeyString(row.ID)
		},
		EncodeValue: func(row runtimeTestRow) ([]byte, error) {
			value := appendValueString(nil, row.Owner)
			value = appendValueString(value, row.Value)
			return value, nil
		},
		DecodeValue: func(primary KeyParts, value []byte) (runtimeTestRow, error) {
			owner, rest, err := readValueString(value)
			if err != nil {
				return runtimeTestRow{}, err
			}
			val, rest, err := readValueString(rest)
			if err != nil {
				return runtimeTestRow{}, err
			}
			if len(rest) != 0 {
				return runtimeTestRow{}, dberrors.ErrCorruptValue
			}
			return runtimeTestRow{ID: primary[0].S, Owner: owner, Value: val}, nil
		},
	})
	if err != nil {
		t.Fatalf("register test table: %v", err)
	}
	return table
}

func TestTableRuntimePrimaryCRUD(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	table := newRuntimeTestTable(t)
	shard := store.db.HashSlot(3)
	ctx := context.Background()

	row := runtimeTestRow{ID: "a", Owner: "o1", Value: "v1"}
	if err := table.Create(ctx, shard, row); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if err := table.Create(ctx, shard, row); err != dberrors.ErrAlreadyExists {
		t.Fatalf("duplicate Create() = %v, want ErrAlreadyExists", err)
	}

	got, ok, err := table.Get(ctx, shard, KeyParts{String("a")})
	if err != nil || !ok || got != row {
		t.Fatalf("Get() = %#v ok=%v err=%v", got, ok, err)
	}

	updated := runtimeTestRow{ID: "a", Owner: "o1", Value: "v2"}
	if err := table.Update(ctx, shard, updated); err != nil {
		t.Fatalf("Update(): %v", err)
	}
	got, ok, err = table.Get(ctx, shard, KeyParts{String("a")})
	if err != nil || !ok || got.Value != "v2" {
		t.Fatalf("Get after update = %#v ok=%v err=%v", got, ok, err)
	}

	if err := table.Update(ctx, shard, runtimeTestRow{ID: "missing"}); err != dberrors.ErrNotFound {
		t.Fatalf("missing Update() = %v, want ErrNotFound", err)
	}

	if err := table.Delete(ctx, shard, KeyParts{String("a")}); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	_, ok, err = table.Get(ctx, shard, KeyParts{String("a")})
	if err != nil || ok {
		t.Fatalf("Get deleted ok=%v err=%v", ok, err)
	}
}

func TestTableRuntimeIndexMaintenanceAndScan(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	table := newRuntimeTestTable(t)
	shard := store.db.HashSlot(5)
	ctx := context.Background()

	if err := table.Upsert(ctx, shard, runtimeTestRow{ID: "a", Owner: "o1", Value: "v1"}); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if err := table.Upsert(ctx, shard, runtimeTestRow{ID: "b", Owner: "o1", Value: "v2"}); err != nil {
		t.Fatalf("upsert b: %v", err)
	}
	rows, cursor, done, err := table.ScanIndex(ctx, shard, 2, KeyParts{String("o1")}, nil, 10)
	if err != nil || !done || len(rows) != 2 || rows[0].ID != "a" || rows[1].ID != "b" || len(cursor) != 0 {
		t.Fatalf("owner scan rows=%#v cursor=%#v done=%v err=%v", rows, cursor, done, err)
	}

	if err := table.Upsert(ctx, shard, runtimeTestRow{ID: "a", Owner: "o2", Value: "v1"}); err != nil {
		t.Fatalf("move owner: %v", err)
	}
	rows, _, done, err = table.ScanIndex(ctx, shard, 2, KeyParts{String("o1")}, nil, 10)
	if err != nil || !done || len(rows) != 1 || rows[0].ID != "b" {
		t.Fatalf("old owner scan rows=%#v done=%v err=%v", rows, done, err)
	}
}

func TestTableRuntimeScanIndexAll(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	table := newRuntimeTestTable(t)
	shard := store.db.HashSlot(32)
	ctx := context.Background()

	if err := table.Upsert(ctx, shard, runtimeTestRow{ID: "a", Owner: "o", Value: "v1"}); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if err := table.Upsert(ctx, shard, runtimeTestRow{ID: "b", Owner: "o", Value: "v2"}); err != nil {
		t.Fatalf("upsert b: %v", err)
	}

	rows, err := table.ScanIndexAll(ctx, shard, 2, KeyParts{String("o")})
	if err != nil || len(rows) != 2 {
		t.Fatalf("ScanIndexAll rows=%#v err=%v", rows, err)
	}
}

func TestTableRuntimeUniqueIndexConflict(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	table := newRuntimeTestTable(t)
	shard := store.db.HashSlot(6)
	ctx := context.Background()

	if err := table.Create(ctx, shard, runtimeTestRow{ID: "a", Owner: "o1", Value: "same"}); err != nil {
		t.Fatalf("create a: %v", err)
	}
	err := table.Create(ctx, shard, runtimeTestRow{ID: "b", Owner: "o2", Value: "same"})
	if err != dberrors.ErrAlreadyExists {
		t.Fatalf("unique conflict err = %v, want ErrAlreadyExists", err)
	}
}

func TestTableRuntimeUniqueIndexIgnoresStaleEntry(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	table := newRuntimeTestTable(t)
	shard := store.db.HashSlot(16)
	ctx := context.Background()

	staleKey, err := encodeTableIndexKey(shard.HashSlot(), table.Schema().ID, 3, KeyParts{String("same")}, KeyParts{String("ghost")})
	if err != nil {
		t.Fatalf("stale unique index key: %v", err)
	}
	batch := store.engine.NewBatch()
	if err := batch.Set(staleKey, nil); err != nil {
		t.Fatalf("set stale unique index: %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("commit stale unique index: %v", err)
	}
	batch.Close()

	if err := table.Create(ctx, shard, runtimeTestRow{ID: "a", Owner: "o", Value: "same"}); err != nil {
		t.Fatalf("Create with stale unique index: %v", err)
	}
}

func TestTableRuntimeIndexScanSkipsStaleEntries(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	table := newRuntimeTestTable(t)
	shard := store.db.HashSlot(7)
	ctx := context.Background()

	if err := table.Upsert(ctx, shard, runtimeTestRow{ID: "a", Owner: "current", Value: "v1"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	staleKey, err := encodeTableIndexKey(shard.HashSlot(), table.Schema().ID, 2, KeyParts{String("stale"), String("a")}, KeyParts{String("a")})
	if err != nil {
		t.Fatalf("stale index key: %v", err)
	}
	batch := store.engine.NewBatch()
	if err := batch.Set(staleKey, nil); err != nil {
		t.Fatalf("set stale index: %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("commit stale index: %v", err)
	}
	batch.Close()

	rows, _, done, err := table.ScanIndex(ctx, shard, 2, KeyParts{String("stale")}, nil, 10)
	if err != nil || !done || len(rows) != 0 {
		t.Fatalf("stale scan rows=%#v done=%v err=%v", rows, done, err)
	}
}

func TestTableRuntimeBatchStaging(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	table := newRuntimeTestTable(t)
	ctx := context.Background()

	batch := store.db.NewBatch()
	if err := table.StageCreate(batch, 8, runtimeTestRow{ID: "a", Owner: "o1", Value: "v1"}); err != nil {
		t.Fatalf("StageCreate: %v", err)
	}
	if err := table.StageUpdate(batch, 8, runtimeTestRow{ID: "a", Owner: "o2", Value: "v2"}); err != nil {
		t.Fatalf("StageUpdate: %v", err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	got, ok, err := table.Get(ctx, store.db.HashSlot(8), KeyParts{String("a")})
	if err != nil || !ok || got.Owner != "o2" || got.Value != "v2" {
		t.Fatalf("Get staged row = %#v ok=%v err=%v", got, ok, err)
	}
	rows, _, done, err := table.ScanIndex(ctx, store.db.HashSlot(8), 2, KeyParts{String("o1")}, nil, 10)
	if err != nil || !done || len(rows) != 0 {
		t.Fatalf("old index rows=%#v done=%v err=%v", rows, done, err)
	}
}

func TestTableRuntimeBatchCreateDuplicateRollsBack(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	table := newRuntimeTestTable(t)
	ctx := context.Background()

	batch := store.db.NewBatch()
	_ = table.StageCreate(batch, 9, runtimeTestRow{ID: "a", Owner: "o", Value: "v1"})
	_ = table.StageCreate(batch, 9, runtimeTestRow{ID: "a", Owner: "o", Value: "v2"})
	if err := batch.Commit(ctx); err != dberrors.ErrAlreadyExists {
		t.Fatalf("Commit duplicate err = %v, want ErrAlreadyExists", err)
	}
	if _, ok, err := table.Get(ctx, store.db.HashSlot(9), KeyParts{String("a")}); err != nil || ok {
		t.Fatalf("duplicate rollback get ok=%v err=%v", ok, err)
	}
}

func TestTableRuntimeBatchUniqueIndexUsesStagedDeletes(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	table := newRuntimeTestTable(t)
	shard := store.db.HashSlot(17)
	ctx := context.Background()

	if err := table.Create(ctx, shard, runtimeTestRow{ID: "a", Owner: "o1", Value: "same"}); err != nil {
		t.Fatalf("Create a: %v", err)
	}

	batch := store.db.NewBatch()
	if err := table.StageDelete(batch, shard.HashSlot(), KeyParts{String("a")}); err != nil {
		t.Fatalf("StageDelete: %v", err)
	}
	if err := table.StageCreate(batch, shard.HashSlot(), runtimeTestRow{ID: "b", Owner: "o2", Value: "same"}); err != nil {
		t.Fatalf("StageCreate: %v", err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if _, ok, err := table.Get(ctx, shard, KeyParts{String("a")}); err != nil || ok {
		t.Fatalf("deleted row a ok=%v err=%v", ok, err)
	}
	if got, ok, err := table.Get(ctx, shard, KeyParts{String("b")}); err != nil || !ok || got.Value != "same" {
		t.Fatalf("created row b = %#v ok=%v err=%v", got, ok, err)
	}
}

func TestTableRuntimePrimaryScan(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	table := newRuntimeTestTable(t)
	shard := store.db.HashSlot(4)
	ctx := context.Background()

	for _, id := range []string{"a", "b", "c"} {
		if err := table.Upsert(ctx, shard, runtimeTestRow{ID: id, Owner: "o", Value: id}); err != nil {
			t.Fatalf("Upsert(%s): %v", id, err)
		}
	}
	rows, cursor, done, err := table.ScanPrimary(ctx, shard, nil, 2)
	if err != nil || done || len(rows) != 2 || rows[0].ID != "a" || rows[1].ID != "b" || !cursor.Equal(KeyParts{String("b")}) {
		t.Fatalf("first page rows=%#v cursor=%#v done=%v err=%v", rows, cursor, done, err)
	}
	rows, cursor, done, err = table.ScanPrimary(ctx, shard, cursor, 2)
	if err != nil || !done || len(rows) != 1 || rows[0].ID != "c" || len(cursor) != 0 {
		t.Fatalf("second page rows=%#v cursor=%#v done=%v err=%v", rows, cursor, done, err)
	}
}

func TestTableRuntimeScanIndexRejectsOversizedPrefix(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	table := newRuntimeTestTable(t)
	shard := store.db.HashSlot(18)
	ctx := context.Background()

	_, _, _, err := table.ScanIndex(ctx, shard, 2, KeyParts{String("o"), String("a"), String("extra")}, nil, 1)
	if err != dberrors.ErrInvalidArgument {
		t.Fatalf("ScanIndex oversized prefix err = %v, want ErrInvalidArgument", err)
	}
}

func TestTableRuntimeScanIndexZeroLimitValidatesIndex(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	table := newRuntimeTestTable(t)
	shard := store.db.HashSlot(19)
	ctx := context.Background()

	_, _, _, err := table.ScanIndex(ctx, shard, 99, nil, nil, 0)
	if err != dberrors.ErrInvalidArgument {
		t.Fatalf("ScanIndex zero limit invalid index err = %v, want ErrInvalidArgument", err)
	}
}

func TestTableRuntimeStageDeletePrimaryFromIndexRemovesOrphanIndex(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()

	indexKey := encodeChannelIDIndexKey(36, "batch-orphan", 7)
	engineBatch := store.engine.NewBatch()
	if err := engineBatch.Set(indexKey, nil); err != nil {
		t.Fatalf("set orphan index: %v", err)
	}
	if err := engineBatch.Commit(true); err != nil {
		t.Fatalf("commit orphan index: %v", err)
	}
	engineBatch.Close()

	batch := store.db.NewBatch()
	if err := channelTable.StageDelete(batch, 36, KeyParts{String("batch-orphan"), Int64Ordered(7)}); err != nil {
		t.Fatalf("StageDelete: %v", err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, ok, err := store.db.get(indexKey); err != nil || ok {
		t.Fatalf("orphan index after staged delete ok=%v err=%v", ok, err)
	}
}

func TestTableRuntimeStageDeletePrimaryFromIndexRemovesCorruptPrimary(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()

	primaryKey := encodeChannelRowKey(37, "batch-corrupt", 8, channelPrimaryFamilyID)
	indexKey := encodeChannelIDIndexKey(37, "batch-corrupt", 8)
	engineBatch := store.engine.NewBatch()
	if err := engineBatch.Set(primaryKey, []byte{1}); err != nil {
		t.Fatalf("set corrupt primary: %v", err)
	}
	if err := engineBatch.Set(indexKey, nil); err != nil {
		t.Fatalf("set channel id index: %v", err)
	}
	if err := engineBatch.Commit(true); err != nil {
		t.Fatalf("commit corrupt primary: %v", err)
	}
	engineBatch.Close()

	batch := store.db.NewBatch()
	if err := channelTable.StageDelete(batch, 37, KeyParts{String("batch-corrupt"), Int64Ordered(8)}); err != nil {
		t.Fatalf("StageDelete: %v", err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, ok, err := store.db.get(primaryKey); err != nil || ok {
		t.Fatalf("corrupt primary after staged delete ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.db.get(indexKey); err != nil || ok {
		t.Fatalf("index after staged delete ok=%v err=%v", ok, err)
	}
}

func TestTableRuntimeStageCreatePrimaryFromIndexRejectsCorruptExistingPrimary(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()

	primaryKey := encodeChannelRowKey(41, "batch-create-corrupt", 9, channelPrimaryFamilyID)
	engineBatch := store.engine.NewBatch()
	if err := engineBatch.Set(primaryKey, []byte{1}); err != nil {
		t.Fatalf("set corrupt primary: %v", err)
	}
	if err := engineBatch.Commit(true); err != nil {
		t.Fatalf("commit corrupt primary: %v", err)
	}
	engineBatch.Close()

	batch := store.db.NewBatch()
	if err := channelTable.StageCreate(batch, 41, Channel{ChannelID: "batch-create-corrupt", ChannelType: 9}); err != nil {
		t.Fatalf("StageCreate: %v", err)
	}
	if err := batch.Commit(ctx); err != dberrors.ErrAlreadyExists {
		t.Fatalf("Commit err = %v, want ErrAlreadyExists", err)
	}
}

func TestTableRuntimeStageUpsertPrimaryFromIndexOverwritesCorruptPrimary(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()

	primaryKey := encodeChannelRowKey(42, "batch-upsert-corrupt", 10, channelPrimaryFamilyID)
	engineBatch := store.engine.NewBatch()
	if err := engineBatch.Set(primaryKey, []byte{1}); err != nil {
		t.Fatalf("set corrupt primary: %v", err)
	}
	if err := engineBatch.Commit(true); err != nil {
		t.Fatalf("commit corrupt primary: %v", err)
	}
	engineBatch.Close()

	channel := Channel{ChannelID: "batch-upsert-corrupt", ChannelType: 10, Ban: 11}
	batch := store.db.NewBatch()
	if err := channelTable.StageUpsert(batch, 42, channel); err != nil {
		t.Fatalf("StageUpsert: %v", err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	got, ok, err := store.db.HashSlot(42).GetChannel(ctx, channel.ChannelID, channel.ChannelType)
	if err != nil || !ok || got != channel {
		t.Fatalf("GetChannel after staged upsert = %+v ok=%v err=%v, want %+v", got, ok, err, channel)
	}
}

func TestTableRuntimeStageUpdatePrimaryFromIndexOverwritesCorruptPrimary(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()

	primaryKey := encodeChannelRowKey(43, "batch-update-corrupt", 11, channelPrimaryFamilyID)
	engineBatch := store.engine.NewBatch()
	if err := engineBatch.Set(primaryKey, []byte{1}); err != nil {
		t.Fatalf("set corrupt primary: %v", err)
	}
	if err := engineBatch.Commit(true); err != nil {
		t.Fatalf("commit corrupt primary: %v", err)
	}
	engineBatch.Close()

	channel := Channel{ChannelID: "batch-update-corrupt", ChannelType: 11, Ban: 12}
	batch := store.db.NewBatch()
	if err := channelTable.StageUpdate(batch, 43, channel); err != nil {
		t.Fatalf("StageUpdate: %v", err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	got, ok, err := store.db.HashSlot(43).GetChannel(ctx, channel.ChannelID, channel.ChannelType)
	if err != nil || !ok || got != channel {
		t.Fatalf("GetChannel after staged update = %+v ok=%v err=%v, want %+v", got, ok, err, channel)
	}
}

func TestTableRuntimePrimaryFromIndexDerivesKeyFromPrimary(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	table, err := registerMetaTableInRegistry(newMetaTableRegistry(), TableSpec[runtimeTestRow]{
		ID:   65031,
		Name: "primary_from_index_test",
		Columns: []schema.Column{
			{ID: 1, Name: "id", Type: schema.TypeString, Required: true},
			{ID: 2, Name: "value", Type: schema.TypeString},
		},
		Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{2}}},
		Primary: PrimarySpec[runtimeTestRow]{
			IndexID:  1,
			FamilyID: 0,
			Name:     "pk_primary_from_index_test",
			Columns:  []uint16{1},
			Layout:   KeyLayout{KeyString},
			Key:      func(row runtimeTestRow) KeyParts { return KeyParts{String(row.ID)} },
		},
		Indexes: []IndexSpec[runtimeTestRow]{
			{
				ID:               2,
				Name:             "idx_primary_from_index_test_id",
				Columns:          []uint16{1},
				Layout:           KeyLayout{KeyString},
				PrimaryFromIndex: true,
				Key: func(row runtimeTestRow) (KeyParts, bool) {
					return KeyParts{String("wrong-" + row.ID)}, true
				},
			},
		},
		Validate: func(row runtimeTestRow) error {
			return validateKeyString(row.ID)
		},
		EncodeValue: func(row runtimeTestRow) ([]byte, error) {
			return appendValueString(nil, row.Value), nil
		},
		DecodeValue: func(primary KeyParts, value []byte) (runtimeTestRow, error) {
			val, rest, err := readValueString(value)
			if err != nil {
				return runtimeTestRow{}, err
			}
			if len(rest) != 0 {
				return runtimeTestRow{}, dberrors.ErrCorruptValue
			}
			return runtimeTestRow{ID: primary[0].S, Value: val}, nil
		},
	})
	if err != nil {
		t.Fatalf("register primary-from-index test table: %v", err)
	}
	shard := store.db.HashSlot(44)
	ctx := context.Background()
	row := runtimeTestRow{ID: "a", Value: "v1"}

	if err := table.Upsert(ctx, shard, row); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	rows, err := table.ScanIndexAll(ctx, shard, 2, KeyParts{String("a")})
	if err != nil || len(rows) != 1 || rows[0] != row {
		t.Fatalf("ScanIndexAll primary-derived rows=%#v err=%v", rows, err)
	}
	if err := table.Delete(ctx, shard, KeyParts{String("a")}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	indexKey, err := encodeTableIndexScanPrefix(44, table.Schema().ID, 2, KeyParts{String("a")})
	if err != nil {
		t.Fatalf("index key: %v", err)
	}
	if _, ok, err := store.db.get(indexKey); err != nil || ok {
		t.Fatalf("primary-derived index after delete ok=%v err=%v", ok, err)
	}
}
