package meta

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestInspectTablesIncludesRegisteredSchemas(t *testing.T) {
	got := InspectTables()
	names := make(map[string]bool, len(got))
	for _, table := range got {
		names[table.Name] = true
	}
	for _, name := range []string{
		"user",
		"device",
		"channel",
		"channel_runtime_meta",
		"subscriber",
		"conversation",
		"cmd_conversation",
		"plugin_binding",
		"channel_migration",
		"hashslot_migration",
	} {
		if !names[name] {
			t.Fatalf("InspectTables() missing %q; got %+v", name, got)
		}
	}
}

func TestInspectScanUserByHashSlot(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	user := User{UID: "u7", Token: "tk7", DeviceFlag: 1, DeviceLevel: 2}
	if err := store.db.HashSlot(7).CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}

	got, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table:       "user",
		HashSlot:    7,
		HashSlotSet: true,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("InspectScan(): %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows len = %d, want 1: %+v", len(got.Rows), got.Rows)
	}
	if got.Rows[0]["uid"] != "u7" || got.Rows[0]["token"] != "tk7" {
		t.Fatalf("row = %+v, want uid/token", got.Rows[0])
	}
	if got.ScannedRows != 1 {
		t.Fatalf("ScannedRows = %d, want 1", got.ScannedRows)
	}
	if !got.Done {
		t.Fatalf("Done = false, want true")
	}
}

func TestInspectScanUserLocalBoundedLimitAcrossHashSlots(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	for _, slot := range []HashSlot{1, 2, 3} {
		user := User{UID: "u" + string(rune('0'+slot)), Token: "tk"}
		if err := store.db.HashSlot(slot).CreateUser(ctx, user); err != nil {
			t.Fatalf("CreateUser(slot %d): %v", slot, err)
		}
	}

	got, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table:         "user",
		HashSlotCount: 4,
		Limit:         2,
	})
	if err != nil {
		t.Fatalf("InspectScan(): %v", err)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("rows len = %d, want 2: %+v", len(got.Rows), got.Rows)
	}
	if got.Done {
		t.Fatalf("Done = true, want false")
	}
	if got.Next == nil {
		t.Fatalf("Next = nil, want cursor")
	}
}

func TestInspectScanUserLocalFilteredScanContinuesPastFilteredRows(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	fixtures := []struct {
		slot HashSlot
		user User
	}{
		{slot: 1, user: User{UID: "u1a", Token: "skip"}},
		{slot: 1, user: User{UID: "u1b", Token: "skip"}},
		{slot: 2, user: User{UID: "u2", Token: "skip"}},
		{slot: 3, user: User{UID: "u3", Token: "match"}},
	}
	for _, fixture := range fixtures {
		if err := store.db.HashSlot(fixture.slot).CreateUser(ctx, fixture.user); err != nil {
			t.Fatalf("CreateUser(slot %d, uid %s): %v", fixture.slot, fixture.user.UID, err)
		}
	}

	got, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table:         "user",
		HashSlotCount: 4,
		Filters:       map[string]any{"token": "match"},
		Limit:         1,
	})
	if err != nil {
		t.Fatalf("InspectScan(): %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows len = %d, want 1: %+v", len(got.Rows), got.Rows)
	}
	if got.Rows[0]["uid"] != "u3" || got.Rows[0]["token"] != "match" {
		t.Fatalf("row = %+v, want slot 3 match", got.Rows[0])
	}
	if !got.Done {
		t.Fatalf("Done = false, want true")
	}
	if got.Next != nil {
		t.Fatalf("Next = %+v, want nil", got.Next)
	}
	if got.ScannedRows != 4 {
		t.Fatalf("ScannedRows = %d, want 4", got.ScannedRows)
	}
}

func TestInspectScanChannelByFilter(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	channel := Channel{
		ChannelID:                 "g1",
		ChannelType:               2,
		Ban:                       1,
		Disband:                   0,
		SendBan:                   1,
		AllowStranger:             1,
		SubscriberMutationVersion: 42,
	}
	if err := store.db.HashSlot(7).UpsertChannel(ctx, channel); err != nil {
		t.Fatalf("UpsertChannel(): %v", err)
	}

	got, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table:       "channel",
		HashSlot:    7,
		HashSlotSet: true,
		Filters:     map[string]any{"channel_id": "g1"},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("InspectScan(): %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows len = %d, want 1: %+v", len(got.Rows), got.Rows)
	}
	row := got.Rows[0]
	if row["channel_id"] != "g1" || row["channel_type"] != int64(2) || row["subscriber_mutation_version"] != uint64(42) {
		t.Fatalf("row = %+v, want channel fields", row)
	}
}

func TestInspectScanDeviceByUID(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	device := Device{UID: "u-device", DeviceFlag: 3, Token: "device-token", DeviceLevel: 9}
	if err := store.db.HashSlot(5).UpsertDevice(ctx, device); err != nil {
		t.Fatalf("UpsertDevice(): %v", err)
	}

	got, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table:       "device",
		HashSlot:    5,
		HashSlotSet: true,
		Filters:     map[string]any{"uid": "u-device"},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("InspectScan(): %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows len = %d, want 1: %+v", len(got.Rows), got.Rows)
	}
	row := got.Rows[0]
	if row["device_flag"] != int64(3) || row["token"] != "device-token" || row["device_level"] != int64(9) {
		t.Fatalf("row = %+v, want device fields", row)
	}
}

func TestInspectScanDeviceFilterUsesPrimaryPrefix(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(5)
	for _, device := range []Device{
		{UID: "u-prefix", DeviceFlag: 1, Token: "match"},
		{UID: "z-other", DeviceFlag: 1, Token: "skip"},
	} {
		if err := shard.UpsertDevice(ctx, device); err != nil {
			t.Fatalf("UpsertDevice(%+v): %v", device, err)
		}
	}

	got, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table:       "device",
		HashSlot:    5,
		HashSlotSet: true,
		Filters:     map[string]any{"uid": "u-prefix"},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("InspectScan(): %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows len = %d, want 1: %+v", len(got.Rows), got.Rows)
	}
	if got.ScannedRows != 1 {
		t.Fatalf("ScannedRows = %d, want 1 prefix-bounded decode", got.ScannedRows)
	}
	if got.Rows[0]["uid"] != "u-prefix" {
		t.Fatalf("row = %+v, want u-prefix", got.Rows[0])
	}
}

func TestInspectScanDeviceCursorAcceptsJSONFloat64NumericPart(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(5)
	for _, device := range []Device{
		{UID: "u-device", DeviceFlag: 1, Token: "first"},
		{UID: "u-device", DeviceFlag: 2, Token: "second"},
	} {
		if err := shard.UpsertDevice(ctx, device); err != nil {
			t.Fatalf("UpsertDevice(%+v): %v", device, err)
		}
	}

	got, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table:       "device",
		HashSlot:    5,
		HashSlotSet: true,
		After:       &InspectCursor{HashSlot: 5, Primary: []any{"u-device", float64(1)}},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("InspectScan(): %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows len = %d, want 1: %+v", len(got.Rows), got.Rows)
	}
	if got.Rows[0]["device_flag"] != int64(2) || got.Rows[0]["token"] != "second" {
		t.Fatalf("row = %+v, want resumed second device", got.Rows[0])
	}
}

func TestInspectScanRejectsNonIntegralNumericCursorPart(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)

	_, err := InspectScan(context.Background(), store.db, InspectScanRequest{
		Table:       "device",
		HashSlot:    5,
		HashSlotSet: true,
		After:       &InspectCursor{HashSlot: 5, Primary: []any{"u-device", float64(1.5)}},
		Limit:       10,
	})
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("InspectScan() err = %v, want invalid argument", err)
	}
}

func TestInspectScanSubscriberFilterUsesCompositePrimaryPrefix(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(11)
	for _, subscriber := range []Subscriber{
		{ChannelID: "sub", ChannelType: 2, UID: "u1"},
		{ChannelID: "sub", ChannelType: 2, UID: "u2"},
		{ChannelID: "zzz", ChannelType: 2, UID: "u3"},
	} {
		if err := subscriberTable.Upsert(ctx, shard, subscriber); err != nil {
			t.Fatalf("subscriberTable.Upsert(%+v): %v", subscriber, err)
		}
	}

	got, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table:       "subscriber",
		HashSlot:    11,
		HashSlotSet: true,
		Filters:     map[string]any{"channel_id": "sub", "channel_type": int64(2)},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("InspectScan(): %v", err)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("rows len = %d, want 2: %+v", len(got.Rows), got.Rows)
	}
	if got.ScannedRows != 2 {
		t.Fatalf("ScannedRows = %d, want 2 prefix-bounded decodes", got.ScannedRows)
	}
	for _, row := range got.Rows {
		if row["channel_id"] != "sub" || row["channel_type"] != int64(2) {
			t.Fatalf("row = %+v, want subscriber prefix", row)
		}
	}
}

func TestInspectScanRejectsCursorOutsidePrimaryPrefixFilter(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)

	_, err := InspectScan(context.Background(), store.db, InspectScanRequest{
		Table:       "channel",
		HashSlot:    7,
		HashSlotSet: true,
		Filters:     map[string]any{"channel_id": "g1"},
		After:       &InspectCursor{HashSlot: 7, Primary: []any{"g2", int64(2)}},
		Limit:       10,
	})
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("InspectScan() err = %v, want invalid argument", err)
	}
}

func TestInspectScanConversationNumericFilterMatchesUnsignedRowField(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	if err := conversationTable.Upsert(ctx, store.db.HashSlot(12), UserConversationState{
		UID:         "u-conv",
		ChannelID:   "conv",
		ChannelType: 2,
		ReadSeq:     3,
		ActiveAt:    10,
		UpdatedAt:   11,
	}); err != nil {
		t.Fatalf("conversationTable.Upsert(): %v", err)
	}

	got, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table:       "conversation",
		HashSlot:    12,
		HashSlotSet: true,
		Filters:     map[string]any{"read_seq": int64(3)},
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("InspectScan(): %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows len = %d, want 1: %+v", len(got.Rows), got.Rows)
	}
	if got.Rows[0]["read_seq"] != uint64(3) {
		t.Fatalf("row = %+v, want read_seq 3", got.Rows[0])
	}
}

func TestInspectScanRemainingTablesSmoke(t *testing.T) {
	tests := []struct {
		name   string
		table  string
		slot   HashSlot
		seed   func(context.Context, *Shard) error
		assert func(*testing.T, InspectRow)
	}{
		{
			name:  "subscriber",
			table: "subscriber",
			slot:  11,
			seed: func(ctx context.Context, shard *Shard) error {
				return subscriberTable.Upsert(ctx, shard, Subscriber{ChannelID: "sub", ChannelType: 2, UID: "u-sub"})
			},
			assert: func(t *testing.T, row InspectRow) {
				t.Helper()
				if row["channel_id"] != "sub" || row["channel_type"] != int64(2) || row["uid"] != "u-sub" {
					t.Fatalf("subscriber row = %+v", row)
				}
			},
		},
		{
			name:  "conversation",
			table: "conversation",
			slot:  12,
			seed: func(ctx context.Context, shard *Shard) error {
				return conversationTable.Upsert(ctx, shard, UserConversationState{UID: "u-conv", ChannelID: "conv", ChannelType: 2, ReadSeq: 3, DeletedToSeq: 1, ActiveAt: 10, UpdatedAt: 11})
			},
			assert: func(t *testing.T, row InspectRow) {
				t.Helper()
				if row["uid"] != "u-conv" || row["read_seq"] != uint64(3) || row["updated_at"] != int64(11) {
					t.Fatalf("conversation row = %+v", row)
				}
			},
		},
		{
			name:  "cmd_conversation",
			table: "cmd_conversation",
			slot:  13,
			seed: func(ctx context.Context, shard *Shard) error {
				return cmdConversationTable.Upsert(ctx, shard, CMDConversationState{UID: "u-cmd", ChannelID: "cmd", ChannelType: 2, ReadSeq: 4, DeletedToSeq: 1, ActiveAt: 20, UpdatedAt: 21})
			},
			assert: func(t *testing.T, row InspectRow) {
				t.Helper()
				if row["uid"] != "u-cmd" || row["read_seq"] != uint64(4) || row["active_at"] != int64(20) {
					t.Fatalf("cmd conversation row = %+v", row)
				}
			},
		},
		{
			name:  "plugin_binding",
			table: "plugin_binding",
			slot:  14,
			seed: func(ctx context.Context, shard *Shard) error {
				return shard.BindPluginUser(ctx, PluginUserBinding{UID: "u-plugin", PluginNo: "p1", CreatedAtMS: 100, UpdatedAtMS: 101})
			},
			assert: func(t *testing.T, row InspectRow) {
				t.Helper()
				if row["uid"] != "u-plugin" || row["plugin_no"] != "p1" || row["created_at_ms"] != int64(100) {
					t.Fatalf("plugin binding row = %+v", row)
				}
			},
		},
		{
			name:  "channel_runtime_meta",
			table: "channel_runtime_meta",
			slot:  15,
			seed: func(ctx context.Context, shard *Shard) error {
				_, err := shard.UpsertChannelRuntimeMeta(ctx, ChannelRuntimeMeta{ChannelID: "rt", ChannelType: 2, ChannelEpoch: 5, LeaderEpoch: 6, Replicas: []uint64{1}, ISR: []uint64{1}, Leader: 1, MinISR: 1})
				return err
			},
			assert: func(t *testing.T, row InspectRow) {
				t.Helper()
				if row["channel_id"] != "rt" || row["channel_epoch"] != uint64(5) || row["leader"] != uint64(1) {
					t.Fatalf("runtime meta row = %+v", row)
				}
			},
		},
		{
			name:  "channel_migration",
			table: "channel_migration",
			slot:  16,
			seed: func(ctx context.Context, shard *Shard) error {
				return shard.CreateChannelMigrationTask(ctx, ChannelMigrationTask{
					TaskID:        "task1",
					Kind:          ChannelMigrationKindLeaderTransfer,
					Status:        ChannelMigrationStatusPending,
					Phase:         ChannelMigrationPhaseValidate,
					ChannelID:     "mig",
					ChannelType:   2,
					SourceNode:    1,
					TargetNode:    2,
					DesiredLeader: 2,
					CreatedAtMS:   10,
					UpdatedAtMS:   11,
				})
			},
			assert: func(t *testing.T, row InspectRow) {
				t.Helper()
				if row["channel_id"] != "mig" || row["task_id"] != "task1" || row["target_node"] != uint64(2) {
					t.Fatalf("channel migration row = %+v", row)
				}
			},
		},
		{
			name:  "hashslot_migration",
			table: "hashslot_migration",
			slot:  17,
			seed: func(ctx context.Context, shard *Shard) error {
				return shard.UpsertHashSlotMigrationState(ctx, HashSlotMigrationState{HashSlot: 17, SourceSlot: 1, TargetSlot: 2, Phase: 1, FenceIndex: 3, LastOutboxIndex: 4, LastAckedIndex: 2})
			},
			assert: func(t *testing.T, row InspectRow) {
				t.Helper()
				if row["hash_slot"] != HashSlot(17) || row["source_slot"] != uint64(1) || row["last_acked_index"] != uint64(2) {
					t.Fatalf("hashslot migration row = %+v", row)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := openTestMetaStore(t)
			defer store.close(t)
			ctx := context.Background()
			if err := tt.seed(ctx, store.db.HashSlot(tt.slot)); err != nil {
				t.Fatalf("seed(): %v", err)
			}

			got, err := InspectScan(ctx, store.db, InspectScanRequest{
				Table:       tt.table,
				HashSlot:    tt.slot,
				HashSlotSet: true,
				Limit:       10,
			})
			if err != nil {
				t.Fatalf("InspectScan(): %v", err)
			}
			if len(got.Rows) != 1 {
				t.Fatalf("rows len = %d, want 1: %+v", len(got.Rows), got.Rows)
			}
			tt.assert(t, got.Rows[0])
		})
	}
}

func TestInspectScanRejectsUnknownTable(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)

	_, err := InspectScan(context.Background(), store.db, InspectScanRequest{
		Table:       "unknown",
		HashSlot:    1,
		HashSlotSet: true,
		Limit:       10,
	})
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("InspectScan() err = %v, want invalid argument", err)
	}
}

func TestInspectScanRejectsEmptyUserCursorPrimary(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)

	_, err := InspectScan(context.Background(), store.db, InspectScanRequest{
		Table:       "user",
		HashSlot:    7,
		HashSlotSet: true,
		After:       &InspectCursor{HashSlot: 7, Primary: []any{""}},
		Limit:       10,
	})
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("InspectScan() err = %v, want invalid argument", err)
	}
}

func TestInspectScanRejectsExplicitHashSlotCursorMismatch(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)

	_, err := InspectScan(context.Background(), store.db, InspectScanRequest{
		Table:       "user",
		HashSlot:    7,
		HashSlotSet: true,
		After:       &InspectCursor{HashSlot: 8},
		Limit:       10,
	})
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("InspectScan() err = %v, want invalid argument", err)
	}
}

func TestInspectScanRejectsNilDB(t *testing.T) {
	_, err := InspectScan(context.Background(), nil, InspectScanRequest{
		Table:       "user",
		HashSlot:    1,
		HashSlotSet: true,
		Limit:       10,
	})
	if !errors.Is(err, dberrors.ErrClosed) {
		t.Fatalf("InspectScan() err = %v, want closed", err)
	}
}

func TestInspectScanRejectsClosedDB(t *testing.T) {
	store := openTestMetaStore(t)
	store.close(t)

	_, err := InspectScan(context.Background(), store.db, InspectScanRequest{
		Table:       "user",
		HashSlot:    1,
		HashSlotSet: true,
		Limit:       10,
	})
	if !errors.Is(err, dberrors.ErrClosed) {
		t.Fatalf("InspectScan() err = %v, want closed", err)
	}
}
