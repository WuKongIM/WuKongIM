package meta

import (
	"reflect"
	"testing"
)

// These codecs are the durable compatibility boundary for metadata snapshots,
// Raft apply, and rolling upgrades. Exercise every registered row shape with
// non-zero data so additions cannot silently disappear from persistence.
func TestMetadataValueCodecsRoundTripCompleteRows(t *testing.T) {
	assertMetadataTableRoundTrip(t, "user", userTable, 7, User{
		UID: "u1", Token: "token", DeviceFlag: 2, DeviceLevel: 3,
	}, nil)
	assertMetadataTableRoundTrip(t, "device", deviceTable, 7, Device{
		UID: "u1", DeviceFlag: 2, Token: "device-token", DeviceLevel: 4,
	}, nil)
	assertMetadataTableRoundTrip(t, "channel", channelTable, 7, Channel{
		ChannelID: "c1", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1,
		AllowStranger: 1, Large: 1, SubscriberMutationVersion: 9, SubscriberCount: 8,
		DirectoryProjectionState: DirectoryProjectionPending, DirectoryProjectionGeneration: 5,
	}, nil)
	assertMetadataTableRoundTrip(t, "subscriber", subscriberTable, 7, Subscriber{
		ChannelID: "c1", ChannelType: 2, UID: "u1",
	}, nil)
	assertMetadataTableRoundTrip(t, "runtime meta", channelRuntimeMetaTable, 7, ChannelRuntimeMeta{
		ChannelID: "c1", ChannelType: 2, ChannelEpoch: 3, LeaderEpoch: 4,
		Replicas: []uint64{3, 1, 2, 2}, ISR: []uint64{2, 1, 1}, Leader: 1, MinISR: 2,
		Status: 2, Features: 9, LeaseUntilMS: 1000, RetentionThroughSeq: 77,
		RetentionUpdatedAtMS: 900, WriteFenceToken: "task-1", WriteFenceVersion: 8,
		WriteFenceReason: 2, WriteFenceUntilMS: 1100, RouteGeneration: 10,
		DirectoryGeneration: 6,
	}, func(row ChannelRuntimeMeta) ChannelRuntimeMeta { return NormalizeChannelRuntimeMeta(row) })
	assertMetadataTableRoundTrip(t, "plugin binding", pluginBindingTable, 7, PluginUserBinding{
		UID: "u1", PluginNo: "bot-a", CreatedAtMS: 10, UpdatedAtMS: 11,
	}, nil)
	assertMetadataTableRoundTrip(t, "ordinary membership", userChannelMembershipTable, 7, UserChannelMembership{
		UID: "u1", ChannelID: "c1", ChannelType: 2, JoinSeq: 3, ReadSeq: 4,
		DeletedToSeq: 5, ActivatedAt: 6, Tombstone: true, TombstoneAt: 7,
		SourceVersion: 8, UpdatedAt: 9,
	}, nil)
	assertMetadataTableRoundTrip(t, "command membership", userCMDChannelMembershipTable, 7, UserCMDChannelMembership{
		UID: "u1", CommandChannelID: "c1@cmd", ChannelType: 2, StartSeq: 3,
		AckSeq: 4, Tombstone: true, TombstoneAt: 5, UpdatedAt: 6,
	}, nil)
	assertMetadataTableRoundTrip(t, "channel latest", channelLatestTable, 7, ChannelLatest{
		ChannelID: "c1", ChannelType: 2, LastMessageID: 9, LastMessageSeq: 8,
		LastAt: 7, FromUID: "u1", ClientMsgNo: "client-1", Payload: []byte("payload"), UpdatedAt: 10,
	}, nil)
	assertMetadataTableRoundTrip(t, "message event state", messageEventStateTable, 7, MessageEventState{
		ChannelID: "c1", ChannelType: 2, ClientMsgNo: "client-1", EventKey: "main",
		Status: EventStatusError, LastMsgEventSeq: 3, LastEventID: "event-3",
		LastEventType: EventTypeStreamError, LastVisibility: VisibilityPrivate,
		LastOccurredAt: 8, SnapshotPayload: []byte(`{"kind":"text","text":"hello"}`),
		EndReason: 2, Error: "failed", UpdatedAt: 9,
	}, nil)
	assertMetadataTableRoundTrip(t, "message event cursor", messageEventCursorTable, 7, MessageEventCursor{
		ChannelID: "c1", ChannelType: 2, ClientMsgNo: "client-1", LastMsgEventSeq: 3, UpdatedAt: 9,
	}, nil)
	assertMetadataTableRoundTrip(t, "message event applied", messageEventAppliedTable, 7, MessageEventApplied{
		ChannelID: "c1", ChannelType: 2, ClientMsgNo: "client-1", EventID: "event-3",
		EventKey: "main", MsgEventSeq: 3, Status: EventStatusError, UpdatedAt: 9,
	}, nil)
	assertMetadataTableRoundTrip(t, "person directory task", personDirectoryTaskTable, 7, PersonDirectoryTask{
		ChannelID: "u1@u2", ChannelType: 1, CommittedTail: 11, CreatedAt: 12, Generation: 2,
	}, nil)
	assertMetadataTableRoundTrip(t, "channel migration", channelMigrationTable, 7, ChannelMigrationTask{
		TaskID: "task-1", Kind: ChannelMigrationKindReplicaReplace, Status: ChannelMigrationStatusRunning,
		Phase: ChannelMigrationPhaseWarmCatchUp, ChannelID: "c1", ChannelType: 2,
		SourceNode: 1, TargetNode: 3, BaseChannelEpoch: 4, BaseLeaderEpoch: 5,
		FenceToken: "task-1", FenceVersion: 6, FenceUntilMS: 100,
		OwnerNodeID: 2, OwnerLeaseUntilMS: 110, Attempt: 3, NextRunAtMS: 120,
		BlockerCode: "lag", BlockerMessage: "catching up", LastError: "retry",
		CreatedAtMS: 10, UpdatedAtMS: 11,
		Progress: ChannelMigrationProgress{LeaderLEO: 20, LeaderHW: 19, TargetLEO: 18, LagRecords: 2},
	}, nil)
	assertMetadataTableRoundTrip(t, "hash slot migration", hashSlotMigrationTable, 7, HashSlotMigrationState{
		HashSlot: 7, SourceSlot: 1, TargetSlot: 2, Phase: 2,
		FenceIndex: 30, LastOutboxIndex: 29, LastAckedIndex: 28,
	}, nil)
}

func TestMetadataValueDecodersRejectTruncation(t *testing.T) {
	assertMetadataTableRejectsTruncation(t, "user", userTable, 7, User{UID: "u1", Token: "t", DeviceFlag: 1, DeviceLevel: 2})
	assertMetadataTableRejectsTruncation(t, "device", deviceTable, 7, Device{UID: "u1", DeviceFlag: 1, Token: "t", DeviceLevel: 2})
	assertMetadataTableRejectsTruncation(t, "channel", channelTable, 7, Channel{ChannelID: "c1", ChannelType: 2})
	assertMetadataTableRejectsTruncation(t, "runtime meta", channelRuntimeMetaTable, 7, ChannelRuntimeMeta{ChannelID: "c1", ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, Replicas: []uint64{1}, ISR: []uint64{1}, Leader: 1, MinISR: 1, LeaseUntilMS: 1})
	assertMetadataTableRejectsTruncation(t, "plugin binding", pluginBindingTable, 7, PluginUserBinding{UID: "u1", PluginNo: "p1", CreatedAtMS: 1, UpdatedAtMS: 2})
	assertMetadataTableRejectsTruncation(t, "ordinary membership", userChannelMembershipTable, 7, UserChannelMembership{UID: "u1", ChannelID: "c1", ChannelType: 2})
	assertMetadataTableRejectsTruncation(t, "command membership", userCMDChannelMembershipTable, 7, UserCMDChannelMembership{UID: "u1", CommandChannelID: "c1@cmd", ChannelType: 2})
	assertMetadataTableRejectsTruncation(t, "channel latest", channelLatestTable, 7, ChannelLatest{ChannelID: "c1", ChannelType: 2})
	assertMetadataTableRejectsTruncation(t, "event state", messageEventStateTable, 7, MessageEventState{ChannelID: "c1", ChannelType: 2, ClientMsgNo: "m1", EventKey: "main"})
	assertMetadataTableRejectsTruncation(t, "event cursor", messageEventCursorTable, 7, MessageEventCursor{ChannelID: "c1", ChannelType: 2, ClientMsgNo: "m1"})
	assertMetadataTableRejectsTruncation(t, "event applied", messageEventAppliedTable, 7, MessageEventApplied{ChannelID: "c1", ChannelType: 2, ClientMsgNo: "m1", EventID: "e1", EventKey: "main"})
	assertMetadataTableRejectsTruncation(t, "person directory", personDirectoryTaskTable, 7, PersonDirectoryTask{ChannelID: "u1@u2", ChannelType: 1, CreatedAt: 1, Generation: 1})
	assertMetadataTableRejectsTruncation(t, "channel migration", channelMigrationTable, 7, ChannelMigrationTask{TaskID: "t1", Kind: ChannelMigrationKindLeaderTransfer, Status: ChannelMigrationStatusPending, Phase: ChannelMigrationPhaseValidate, ChannelID: "c1", ChannelType: 2})
	assertMetadataTableRejectsTruncation(t, "hash slot migration", hashSlotMigrationTable, 7, HashSlotMigrationState{HashSlot: 7, SourceSlot: 1, TargetSlot: 2})
}

func TestHashSlotMigrationAuxiliaryCodecsBindIdentityToKeys(t *testing.T) {
	delta := AppliedHashSlotDelta{HashSlot: 7, SourceSlot: 11, SourceIndex: 19}
	prefix := encodeAppliedHashSlotDeltaPrefix(delta.HashSlot)
	decodedDelta, err := decodeAppliedHashSlotDeltaKey(delta.HashSlot, prefix, encodeAppliedHashSlotDeltaKey(delta))
	if err != nil || decodedDelta != delta {
		t.Fatalf("decoded applied delta = %+v, error = %v", decodedDelta, err)
	}

	row := HashSlotMigrationOutboxRow{HashSlot: 7, SourceSlot: 11, TargetSlot: 12, SourceIndex: 19, Data: []byte("command")}
	key := encodeHashSlotMigrationOutboxKey(row.HashSlot, row.SourceSlot, row.TargetSlot, row.SourceIndex)
	decodedRow, err := decodeHashSlotMigrationOutboxValue(row.HashSlot, key, encodeHashSlotMigrationOutboxValue(row))
	if err != nil || !reflect.DeepEqual(decodedRow, row) {
		t.Fatalf("decoded outbox row = %+v, error = %v", decodedRow, err)
	}
	otherKey := encodeHashSlotMigrationOutboxKey(row.HashSlot, row.SourceSlot, row.TargetSlot, row.SourceIndex+1)
	keyBound, err := decodeHashSlotMigrationOutboxValue(row.HashSlot, otherKey, encodeHashSlotMigrationOutboxValue(row))
	if err != nil || keyBound.SourceIndex != row.SourceIndex+1 || !reflect.DeepEqual(keyBound.Data, row.Data) {
		t.Fatalf("key-bound outbox row = %+v, error = %v", keyBound, err)
	}
}

func assertMetadataTableRoundTrip[R any](t *testing.T, name string, table Table[R], hashSlot HashSlot, row R, normalize func(R) R) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		if table.spec.Validate != nil {
			if err := table.spec.Validate(row); err != nil {
				t.Fatalf("validate row: %v", err)
			}
		}
		primary := table.spec.Primary.Key(row)
		key, err := table.primaryRowKey(hashSlot, primary)
		if err != nil {
			t.Fatalf("primary row key: %v", err)
		}
		prefix := encodeRowPrefix(hashSlot, table.spec.ID)
		decodedPrimary, ok := table.decodePrimaryRowKey(prefix, key)
		if !ok || !decodedPrimary.Equal(primary) {
			t.Fatalf("decoded primary = %v ok=%v, want %v", decodedPrimary, ok, primary)
		}
		value, err := table.encodeValue(key, row)
		if err != nil {
			t.Fatalf("encode value: %v", err)
		}
		decoded, err := table.decodeValue(key, primary, value)
		if err != nil {
			t.Fatalf("decode value: %v", err)
		}
		want := row
		if normalize != nil {
			want = normalize(row)
		}
		if !reflect.DeepEqual(decoded, want) {
			t.Fatalf("decoded row = %#v, want %#v", decoded, want)
		}
	})
}

func assertMetadataTableRejectsTruncation[R any](t *testing.T, name string, table Table[R], hashSlot HashSlot, row R) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		primary := table.spec.Primary.Key(row)
		key, err := table.primaryRowKey(hashSlot, primary)
		if err != nil {
			t.Fatalf("primary row key: %v", err)
		}
		value, err := table.encodeValue(key, row)
		if err != nil {
			t.Fatalf("encode value: %v", err)
		}
		if len(value) == 0 {
			t.Skip("table has no persisted value")
		}
		if _, err := table.decodeValue(key, primary, value[:len(value)-1]); err == nil {
			t.Fatal("truncated value decoded successfully")
		}
	})
}
