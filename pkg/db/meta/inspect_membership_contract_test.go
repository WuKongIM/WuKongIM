package meta

import (
	"context"
	"testing"
)

func TestInspectScanPagesBothMembershipDirectoriesWithCompleteState(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	const hashSlot HashSlot = 60
	shard := store.db.HashSlot(hashSlot)

	ordinary := []UserChannelMembership{
		{UID: "alice", ChannelID: "room-a", ChannelType: 2, JoinSeq: 5, ReadSeq: 6, DeletedToSeq: 1, ActivatedAt: 200, SourceVersion: 3, UpdatedAt: 201},
		{UID: "alice", ChannelID: "room-b", ChannelType: 2, JoinSeq: 8, ReadSeq: 9, ActivatedAt: 100, SourceVersion: 4, UpdatedAt: 101},
	}
	for _, row := range ordinary {
		if err := shard.UpsertUserChannelMembership(ctx, row); err != nil {
			t.Fatalf("UpsertUserChannelMembership(%+v): %v", row, err)
		}
	}
	cmdRows := []UserCMDChannelMembership{
		{UID: "alice", CommandChannelID: "room-a_cmd", ChannelType: 2, StartSeq: 10, AckSeq: 12, UpdatedAt: 300},
		{UID: "alice", CommandChannelID: "room-b_cmd", ChannelType: 2, StartSeq: 20, AckSeq: 21, UpdatedAt: 400},
	}
	for _, row := range cmdRows {
		if err := shard.UpsertUserCMDChannelMembership(ctx, row); err != nil {
			t.Fatalf("UpsertUserCMDChannelMembership(%+v): %v", row, err)
		}
	}

	first, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table: "user_channel_membership", HashSlot: hashSlot, HashSlotSet: true,
		Filters: map[string]any{"uid": "alice"}, Limit: 1,
	})
	if err != nil || len(first.Rows) != 1 || first.Done || first.Next == nil {
		t.Fatalf("InspectScan(ordinary first) = (%+v, %v)", first, err)
	}
	second, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table: "user_channel_membership", HashSlot: hashSlot, HashSlotSet: true,
		Filters: map[string]any{"uid": "alice"}, After: first.Next, Limit: 10,
	})
	if err != nil || len(second.Rows) != 1 || second.Rows[0]["channel_id"] == first.Rows[0]["channel_id"] {
		t.Fatalf("InspectScan(ordinary resume) = (%+v, %v)", second, err)
	}
	ordinaryByChannel := map[string]InspectRow{
		first.Rows[0]["channel_id"].(string):  first.Rows[0],
		second.Rows[0]["channel_id"].(string): second.Rows[0],
	}
	if row := ordinaryByChannel["room-a"]; row["join_seq"] != uint64(5) || row["read_seq"] != uint64(6) || row["source_version"] != uint64(3) || row["activated_at"] != int64(200) {
		t.Fatalf("inspected ordinary room-a = %+v", row)
	}

	cmdFirst, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table: "user_cmd_channel_membership", HashSlot: hashSlot, HashSlotSet: true,
		Filters: map[string]any{"uid": "alice"}, Limit: 1,
	})
	if err != nil || len(cmdFirst.Rows) != 1 || cmdFirst.Done || cmdFirst.Next == nil {
		t.Fatalf("InspectScan(CMD first) = (%+v, %v)", cmdFirst, err)
	}
	cmdSecond, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table: "user_cmd_channel_membership", HashSlot: hashSlot, HashSlotSet: true,
		Filters: map[string]any{"uid": "alice"}, After: cmdFirst.Next, Limit: 10,
	})
	if err != nil || len(cmdSecond.Rows) != 1 || cmdSecond.Rows[0]["command_channel_id"] == cmdFirst.Rows[0]["command_channel_id"] {
		t.Fatalf("InspectScan(CMD resume) = (%+v, %v)", cmdSecond, err)
	}
	cmdByChannel := map[string]InspectRow{
		cmdFirst.Rows[0]["command_channel_id"].(string):  cmdFirst.Rows[0],
		cmdSecond.Rows[0]["command_channel_id"].(string): cmdSecond.Rows[0],
	}
	if row := cmdByChannel["room-b_cmd"]; row["start_seq"] != uint64(20) || row["ack_seq"] != uint64(21) || row["updated_at"] != int64(400) {
		t.Fatalf("inspected CMD room-b = %+v", row)
	}
}

func TestInspectScanHashSlotCursorSurvivesJSONNumericRoundTrip(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	const hashSlot HashSlot = 61
	state := HashSlotMigrationState{
		HashSlot: hashSlot, SourceSlot: 11, TargetSlot: 22,
		Phase: 1, FenceIndex: 30, LastOutboxIndex: 40, LastAckedIndex: 20,
	}
	if err := store.db.HashSlot(hashSlot).UpsertHashSlotMigrationState(ctx, state); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(): %v", err)
	}

	first, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table: "hashslot_migration", HashSlot: hashSlot, HashSlotSet: true,
		Filters: map[string]any{"source_slot": float64(11)}, Limit: 1,
	})
	if err != nil || len(first.Rows) != 1 || !first.Done {
		t.Fatalf("InspectScan(first) = (%+v, %v)", first, err)
	}
	if first.Rows[0]["last_acked_index"] != uint64(20) {
		t.Fatalf("inspected migration row = %+v", first.Rows[0])
	}
	// JSON decoders represent the uint8 primary key as float64. The operator
	// cursor must accept that representation without revisiting the state row.
	jsonCursor := &InspectCursor{HashSlot: hashSlot, Primary: []any{float64(hashSlotMigrationRecordState)}}
	resumed, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table: "hashslot_migration", HashSlot: hashSlot, HashSlotSet: true,
		After: jsonCursor, Limit: 1,
	})
	if err != nil || len(resumed.Rows) != 0 || !resumed.Done {
		t.Fatalf("InspectScan(JSON cursor resume) = (%+v, %v), want exhausted", resumed, err)
	}

	filtered, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table: "hashslot_migration", HashSlot: hashSlot, HashSlotSet: true,
		Filters: map[string]any{"source_slot": float64(11.5)}, Limit: 10,
	})
	if err != nil || len(filtered.Rows) != 0 || filtered.ScannedRows != 1 {
		t.Fatalf("InspectScan(non-integral numeric filter) = (%+v, %v)", filtered, err)
	}
}
