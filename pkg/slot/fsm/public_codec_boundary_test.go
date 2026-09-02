package fsm

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestDecodeCommandHashSlotsUsesExactLogicalOwners(t *testing.T) {
	latest := func(channelID string) metadb.ChannelLatest {
		return metadb.ChannelLatest{ChannelID: channelID, ChannelType: 2}
	}
	batch, err := EncodeUpsertChannelLatestBatchCommandChecked([]ChannelLatestBatchItem{
		{HashSlot: 9, Latest: latest("channel-b")},
		{HashSlot: 7, Latest: latest("channel-a")},
		{HashSlot: 9, Latest: latest("channel-c")},
	})
	if err != nil {
		t.Fatalf("encode latest batch: %v", err)
	}
	got, err := DecodeCommandHashSlots(batch, 42)
	if err != nil || !reflect.DeepEqual(got, []uint16{7, 9}) {
		t.Fatalf("batch hash slots = %#v, %v", got, err)
	}
	got[0] = 99
	again, err := DecodeCommandHashSlots(batch, 42)
	if err != nil || !reflect.DeepEqual(again, []uint16{7, 9}) {
		t.Fatalf("hash slot result was aliased: %#v, %v", again, err)
	}
	if got, err := DecodeCommandHashSlots(EncodeNoopCommand(), 42); err != nil || !reflect.DeepEqual(got, []uint16{42}) {
		t.Fatalf("envelope hash slot = %#v, %v", got, err)
	}
	if _, err := DecodeCommandHashSlots([]byte{commandVersion}, 42); err == nil {
		t.Fatal("truncated command unexpectedly yielded hash slots")
	}
}

func TestCreateRuntimeCommandClassifierAndCardinality(t *testing.T) {
	metaCommand, err := EncodeCreateChannelRuntimeMetaBatchCommandChecked([]CreateChannelRuntimeMetaBatchItem{
		{HashSlot: 7, Meta: metadb.ChannelRuntimeMeta{ChannelID: "group-a", ChannelType: 2}},
		{HashSlot: 8, Meta: metadb.ChannelRuntimeMeta{ChannelID: "group-b", ChannelType: 2}},
	})
	if err != nil {
		t.Fatalf("encode runtime batch: %v", err)
	}
	if !IsCreateChannelRuntimeMetaCommand(metaCommand) {
		t.Fatal("runtime create command was not classified")
	}
	if size, err := CreateChannelRuntimeMetaBatchCommandSize(metaCommand); err != nil || size != 2 {
		t.Fatalf("runtime create command size = %d, %v", size, err)
	}

	personChannel := runtimechannelid.EncodePersonChannel("u1", "u2")
	admit, err := EncodeAdmitPersonDirectoryTaskBatchCommandChecked([]PersonDirectoryAdmissionBatchItem{{
		HashSlot: 9,
		Task: metadb.PersonDirectoryTask{
			ChannelID: personChannel, ChannelType: 1,
		},
		RuntimeMeta: metadb.ChannelRuntimeMeta{ChannelID: personChannel, ChannelType: 1},
	}})
	if err != nil {
		t.Fatalf("encode person admission: %v", err)
	}
	if !IsCreateChannelRuntimeMetaCommand(admit) {
		t.Fatal("person admission command was not classified as a create outcome")
	}
	if size, err := CreateChannelRuntimeMetaBatchCommandSize(admit); err != nil || size != 1 {
		t.Fatalf("person admission command size = %d, %v", size, err)
	}
	for _, invalid := range [][]byte{nil, EncodeNoopCommand(), {commandVersion + 1, cmdTypeCreateChannelRuntimeMeta}} {
		if IsCreateChannelRuntimeMetaCommand(invalid) {
			t.Fatalf("invalid command classified as runtime create: %x", invalid)
		}
		if _, err := CreateChannelRuntimeMetaBatchCommandSize(invalid); !errors.Is(err, metadb.ErrInvalidArgument) {
			t.Fatalf("invalid command size error = %v", err)
		}
	}
}

func TestChannelMigrationClassifierRequiresAValidCommandVersion(t *testing.T) {
	command := EncodeCreateChannelMigrationTaskCommand(metadb.ChannelMigrationTask{TaskID: "task-1"})
	if !IsChannelMigrationCommand(command) {
		t.Fatal("channel migration command was not classified")
	}
	wrongVersion := append([]byte(nil), command...)
	wrongVersion[0]++
	for _, invalid := range [][]byte{nil, {commandVersion}, EncodeNoopCommand(), wrongVersion} {
		if IsChannelMigrationCommand(invalid) {
			t.Fatalf("invalid migration command was classified: %x", invalid)
		}
	}
}

func TestCheckedMembershipAndLatestEncodersEnforceProposalBounds(t *testing.T) {
	membership := metadb.UserChannelMembership{UID: "u1", ChannelID: "group-1", ChannelType: 2}
	for name, encode := range map[string]func([]metadb.UserChannelMembership) ([]byte, error){
		"upsert": EncodeUpsertUserChannelMembershipsCommandChecked,
		"delete": EncodeDeleteUserChannelMembershipsCommandChecked,
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := encode([]metadb.UserChannelMembership{membership})
			if err != nil {
				t.Fatalf("valid membership command: %v", err)
			}
			if _, err := decodeCommand(encoded); err != nil {
				t.Fatalf("decode valid membership command: %v", err)
			}
			oversized := make([]metadb.UserChannelMembership, MaxSubscriberCommandUIDs+1)
			for i := range oversized {
				oversized[i] = membership
			}
			if _, err := encode(oversized); !errors.Is(err, metadb.ErrInvalidArgument) {
				t.Fatalf("oversized membership error = %v", err)
			}
		})
	}

	validLatest := metadb.ChannelLatest{ChannelID: "group-1", ChannelType: 2, LastMessageSeq: 9}
	if encoded, err := EncodeUpsertChannelLatestCommandChecked(validLatest); err != nil {
		t.Fatalf("valid latest command: %v", err)
	} else if _, err := decodeCommand(encoded); err != nil {
		t.Fatalf("decode valid latest command: %v", err)
	}
	for _, invalid := range []metadb.ChannelLatest{{ChannelType: 2}, {ChannelID: "group-1"}} {
		if _, err := EncodeUpsertChannelLatestCommandChecked(invalid); !errors.Is(err, metadb.ErrInvalidArgument) {
			t.Fatalf("invalid latest error = %v", err)
		}
	}
	if _, err := EncodeUpsertChannelLatestBatchCommandChecked(nil); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("empty latest batch error = %v", err)
	}
	if _, err := EncodeUpsertChannelLatestBatchCommandChecked([]ChannelLatestBatchItem{{HashSlot: 7}}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("invalid latest batch item error = %v", err)
	}
}

func TestTLVScalarAndSetBoundaries(t *testing.T) {
	for _, value := range []bool{false, true} {
		encoded := appendBoolTLVField(nil, 9, value)
		tag, raw, consumed, err := readTLV(encoded)
		if err != nil || tag != 9 || consumed != len(encoded) {
			t.Fatalf("bool TLV = tag %d raw %x consumed %d err %v", tag, raw, consumed, err)
		}
		decoded, err := decodeBoolTLVValue(raw, "flag")
		if err != nil || decoded != value {
			t.Fatalf("decoded bool = %v, %v; want %v", decoded, err, value)
		}
	}
	for _, raw := range [][]byte{nil, {2}} {
		if _, err := decodeBoolTLVValue(raw, "flag"); !errors.Is(err, metadb.ErrCorruptValue) {
			t.Fatalf("invalid bool %x error = %v", raw, err)
		}
	}
	var hashSlotRaw [8]byte
	binary.BigEndian.PutUint64(hashSlotRaw[:], 65535)
	if got, err := decodeHashSlotTLVValue(hashSlotRaw[:], "hash slot"); err != nil || got != 65535 {
		t.Fatalf("decoded hash slot = %d, %v", got, err)
	}
	for _, raw := range [][]byte{{1}, func() []byte {
		value := make([]byte, 8)
		binary.BigEndian.PutUint64(value, 65536)
		return value
	}()} {
		if _, err := decodeHashSlotTLVValue(raw, "hash slot"); !errors.Is(err, metadb.ErrCorruptValue) {
			t.Fatalf("invalid hash slot %x error = %v", raw, err)
		}
	}

	if got := canonicalizeUint64Set(nil); got != nil {
		t.Fatalf("empty canonical set = %#v", got)
	}
	if got := canonicalizeUint64Set([]uint64{3, 1, 3, 2, 1}); !reflect.DeepEqual(got, []uint64{1, 2, 3}) {
		t.Fatalf("canonical uint64 set = %#v", got)
	}
	if got := decodeStringSet(encodeStringSet([]string{"b", "a", "b"})); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("canonical string set = %#v", got)
	}
	if got := decodeStringSet(nil); got != nil {
		t.Fatalf("empty string set = %#v", got)
	}
}

func TestCommandInspectionCoversSensitiveAndOperationalCommands(t *testing.T) {
	device, err := DecodeCommandInspection(EncodeUpsertDeviceCommand(metadb.Device{
		UID: "u1", DeviceFlag: 2, Token: "secret-device-token", DeviceLevel: 3,
	}))
	if err != nil || device.Type != "upsert_device" || device.Payload["token"] != redactedSecret {
		t.Fatalf("device inspection = %#v, %v", device, err)
	}

	added, err := DecodeCommandInspection(EncodeAddSubscribersCommand("group-1", 2, []string{"u2", "u1"}, 7))
	if err != nil || added.Type != "add_subscribers" || added.Payload["subscriber_mutation_version"] != uint64(7) {
		t.Fatalf("subscriber add inspection = %#v, %v", added, err)
	}
	removed, err := DecodeCommandInspection(EncodeRemoveSubscribersCommand("group-1", 2, []string{"u1"}))
	if err != nil || removed.Type != "remove_subscribers" {
		t.Fatalf("subscriber removal inspection = %#v, %v", removed, err)
	}
	if _, ok := removed.Payload["subscriber_mutation_version"]; ok {
		t.Fatalf("zero mutation version unexpectedly rendered: %#v", removed.Payload)
	}

	guard := metadb.ChannelMigrationTaskGuard{
		TaskID: "task-1", ChannelID: "group-1", ChannelType: 2,
		ExpectedStatus: metadb.ChannelMigrationStatusPending,
		ExpectedPhase:  metadb.ChannelMigrationPhaseValidate,
	}
	claim, err := DecodeCommandInspection(EncodeClaimChannelMigrationTaskCommand(metadb.ChannelMigrationTaskClaim{Guard: guard}))
	if err != nil || claim.Type != "claim_channel_migration_task" || claim.Payload["task_id"] != "task-1" {
		t.Fatalf("migration guard inspection = %#v, %v", claim, err)
	}

	tests := []struct {
		want string
		data []byte
	}{
		{want: "noop", data: EncodeNoopCommand()},
		{want: "delete_channel", data: EncodeDeleteChannelCommand("group-1", 2)},
		{want: "delete_channel_runtime_meta", data: EncodeDeleteChannelRuntimeMetaCommand("group-1", 2)},
		{want: "enter_fence", data: EncodeEnterFenceCommandForTarget(7, multiraft.SlotID(9))},
		{want: "ack_migration_outbox", data: EncodeAckHashSlotMigrationOutboxCommand(7, 8, 9, 10)},
		{want: "cleanup_migration_outbox", data: EncodeCleanupHashSlotMigrationOutboxCommand(7, 8, 9, 10)},
	}
	for _, test := range tests {
		t.Run(test.want, func(t *testing.T) {
			inspection, err := DecodeCommandInspection(test.data)
			if err != nil || inspection.Type != test.want || inspection.Payload["command"] != test.want {
				t.Fatalf("inspection = %#v, %v", inspection, err)
			}
		})
	}

	keys := conversationKeysPayload([]metadb.ChannelKey{{ChannelID: "a", ChannelType: 1}, {ChannelID: "b", ChannelType: 2}})
	if len(keys) != 2 || keys[1]["channel_id"] != "b" || keys[1]["channel_type"] != int64(2) {
		t.Fatalf("conversation key inspection = %#v", keys)
	}
	if _, err := DecodeCommandInspection(EncodeUpsertChannelLatestCommand(metadb.ChannelLatest{ChannelID: "group-1", ChannelType: 2})); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("unsupported inspection error = %v", err)
	}
}
