package fsm

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestStateMachineEncodeUpsertCommands(t *testing.T) {
	userCmd := EncodeUpsertUserCommand(metadb.User{UID: "u1", Token: "t1", DeviceFlag: 3, DeviceLevel: 7})
	decoded, err := decodeCommand(userCmd)
	if err != nil {
		t.Fatalf("decodeCommand(user) error = %v", err)
	}
	uc, ok := decoded.(*upsertUserCmd)
	if !ok {
		t.Fatalf("decodeCommand(user) type = %T, want *upsertUserCmd", decoded)
	}
	if uc.user.UID != "u1" || uc.user.Token != "t1" || uc.user.DeviceFlag != 3 || uc.user.DeviceLevel != 7 {
		t.Fatalf("decoded user = %+v", uc.user)
	}

	channelCmd := EncodeUpsertChannelCommand(metadb.Channel{ChannelID: "c1", ChannelType: 1, Ban: 1})
	decoded, err = decodeCommand(channelCmd)
	if err != nil {
		t.Fatalf("decodeCommand(channel) error = %v", err)
	}
	cc, ok := decoded.(*upsertChannelCmd)
	if !ok {
		t.Fatalf("decodeCommand(channel) type = %T, want *upsertChannelCmd", decoded)
	}
	if cc.channel.ChannelID != "c1" || cc.channel.ChannelType != 1 || cc.channel.Ban != 1 {
		t.Fatalf("decoded channel = %+v", cc.channel)
	}

	metaCmd := EncodeUpsertChannelRuntimeMetaCommand(metadb.ChannelRuntimeMeta{
		ChannelID:    "c1",
		ChannelType:  1,
		ChannelEpoch: 3,
		LeaderEpoch:  2,
		Replicas:     []uint64{3, 1, 2},
		ISR:          []uint64{2, 1},
		Leader:       1,
		MinISR:       2,
		Status:       3,
		Features:     9,
		LeaseUntilMS: 1700000000000,
	})
	decoded, err = decodeCommand(metaCmd)
	if err != nil {
		t.Fatalf("decodeCommand(runtime_meta) error = %v", err)
	}
	mc, ok := decoded.(*upsertChannelRuntimeMetaCmd)
	if !ok {
		t.Fatalf("decodeCommand(runtime_meta) type = %T, want *upsertChannelRuntimeMetaCmd", decoded)
	}
	wantMeta := metadb.ChannelRuntimeMeta{
		ChannelID:    "c1",
		ChannelType:  1,
		ChannelEpoch: 3,
		LeaderEpoch:  2,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2},
		Leader:       1,
		MinISR:       2,
		Status:       3,
		Features:     9,
		LeaseUntilMS: 1700000000000,
	}
	if !reflect.DeepEqual(mc.meta, wantMeta) {
		t.Fatalf("decoded runtime meta = %#v, want %#v", mc.meta, wantMeta)
	}

	createUserData := EncodeCreateUserCommand(metadb.User{UID: "u-create", Token: "create-token", DeviceFlag: 4, DeviceLevel: 8})
	decoded, err = decodeCommand(createUserData)
	if err != nil {
		t.Fatalf("decodeCommand(create_user) error = %v", err)
	}
	cuc, ok := decoded.(*createUserCmd)
	if !ok {
		t.Fatalf("decodeCommand(create_user) type = %T, want *createUserCmd", decoded)
	}
	if cuc.user.UID != "u-create" || cuc.user.Token != "create-token" || cuc.user.DeviceFlag != 4 || cuc.user.DeviceLevel != 8 {
		t.Fatalf("decoded create user = %+v", cuc.user)
	}

	deviceData := EncodeUpsertDeviceCommand(metadb.Device{UID: "u-device", DeviceFlag: 6, Token: "device-token", DeviceLevel: 9})
	decoded, err = decodeCommand(deviceData)
	if err != nil {
		t.Fatalf("decodeCommand(device) error = %v", err)
	}
	dc, ok := decoded.(*upsertDeviceCmd)
	if !ok {
		t.Fatalf("decodeCommand(device) type = %T, want *upsertDeviceCmd", decoded)
	}
	if dc.device.UID != "u-device" || dc.device.DeviceFlag != 6 || dc.device.Token != "device-token" || dc.device.DeviceLevel != 9 {
		t.Fatalf("decoded device = %+v", dc.device)
	}

	applyDeltaData := EncodeApplyDeltaCommand(11, 7, 5, EncodeUpsertUserCommand(metadb.User{UID: "u-delta", Token: "delta"}))
	decoded, err = decodeCommand(applyDeltaData)
	if err != nil {
		t.Fatalf("decodeCommand(apply_delta) error = %v", err)
	}
	applyDelta, ok := decoded.(*applyDeltaCmd)
	if !ok {
		t.Fatalf("decodeCommand(apply_delta) type = %T, want *applyDeltaCmd", decoded)
	}
	if applyDelta.SourceSlotID != 11 || applyDelta.SourceIndex != 7 || applyDelta.HashSlot != 5 {
		t.Fatalf("decoded apply delta = %#v", applyDelta)
	}

	enterFenceData := EncodeEnterFenceCommand(5)
	decoded, err = decodeCommand(enterFenceData)
	if err != nil {
		t.Fatalf("decodeCommand(enter_fence) error = %v", err)
	}
	enterFence, ok := decoded.(*enterFenceCmd)
	if !ok {
		t.Fatalf("decodeCommand(enter_fence) type = %T, want *enterFenceCmd", decoded)
	}
	if enterFence.HashSlot != 5 {
		t.Fatalf("decoded enter fence = %#v", enterFence)
	}
}

func TestStateMachineApplyUpsertsUserAndChannel(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	result, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  1,
		Term:   1,
		Data:   EncodeUpsertUserCommand(metadb.User{UID: "u1", Token: "t1", DeviceFlag: 1, DeviceLevel: 2}),
	})
	if err != nil {
		t.Fatalf("Apply(user create) error = %v", err)
	}
	if string(result) != ApplyResultOK {
		t.Fatalf("Apply(user create) result = %q, want %q", result, ApplyResultOK)
	}

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  2,
		Term:   1,
		Data:   EncodeUpsertUserCommand(metadb.User{UID: "u1", Token: "t2", DeviceFlag: 5, DeviceLevel: 9}),
	}); err != nil {
		t.Fatalf("Apply(user update) error = %v", err)
	}

	gotUser, err := db.ForSlot(11).GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if gotUser.Token != "t2" || gotUser.DeviceFlag != 5 || gotUser.DeviceLevel != 9 {
		t.Fatalf("updated user = %#v", gotUser)
	}

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  3,
		Term:   1,
		Data:   EncodeUpsertChannelCommand(metadb.Channel{ChannelID: "c1", ChannelType: 1, Ban: 1}),
	}); err != nil {
		t.Fatalf("Apply(channel create) error = %v", err)
	}

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  4,
		Term:   1,
		Data:   EncodeUpsertChannelCommand(metadb.Channel{ChannelID: "c1", ChannelType: 1, Ban: 9}),
	}); err != nil {
		t.Fatalf("Apply(channel update) error = %v", err)
	}

	gotChannel, err := db.ForSlot(11).GetChannel(ctx, "c1", 1)
	if err != nil {
		t.Fatalf("GetChannel() error = %v", err)
	}
	if gotChannel.Ban != 9 {
		t.Fatalf("updated channel = %#v", gotChannel)
	}
}

func TestStateMachineApplyUpsertsAndDeletesChannelRuntimeMeta(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    "c-meta",
		ChannelType:  4,
		ChannelEpoch: 7,
		LeaderEpoch:  5,
		Replicas:     []uint64{3, 2, 1},
		ISR:          []uint64{2, 1},
		Leader:       2,
		MinISR:       2,
		Status:       4,
		Features:     12,
		LeaseUntilMS: 1700000001234,
	}

	result, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  1,
		Term:   1,
		Data:   EncodeUpsertChannelRuntimeMetaCommand(meta),
	})
	if err != nil {
		t.Fatalf("Apply(upsert runtime meta) error = %v", err)
	}
	if string(result) != ApplyResultOK {
		t.Fatalf("Apply(upsert runtime meta) result = %q, want %q", result, ApplyResultOK)
	}

	got, err := db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	want := meta
	want.Replicas = []uint64{1, 2, 3}
	want.ISR = []uint64{1, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stored runtime meta = %#v, want %#v", got, want)
	}

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  2,
		Term:   1,
		Data:   EncodeDeleteChannelRuntimeMetaCommand(meta.ChannelID, meta.ChannelType),
	}); err != nil {
		t.Fatalf("Apply(delete runtime meta) error = %v", err)
	}

	_, err = db.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetChannelRuntimeMeta() err = %v, want ErrNotFound", err)
	}
}

func TestStateMachineAppliesAddAndRemoveSubscribers(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	result, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  1,
		Term:   1,
		Data:   encodeTestAddSubscribersCommand("slot-1", 2, []string{"u3", "u1", "u2"}),
	})
	if err != nil {
		t.Fatalf("Apply(add subscribers) error = %v", err)
	}
	if string(result) != ApplyResultOK {
		t.Fatalf("Apply(add subscribers) result = %q, want %q", result, ApplyResultOK)
	}

	shard, ok := any(db.ForSlot(11)).(interface {
		ListSubscribersPage(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error)
	})
	if !ok {
		t.Fatalf("subscriber shard store methods missing")
	}

	got, _, done, err := shard.ListSubscribersPage(ctx, "slot-1", 2, "", 10)
	if err != nil {
		t.Fatalf("ListSubscribersPage() after add error = %v", err)
	}
	if !reflect.DeepEqual(got, []string{"u1", "u2", "u3"}) {
		t.Fatalf("subscribers after add = %#v", got)
	}
	if !done {
		t.Fatalf("done after add = false, want true")
	}

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  2,
		Term:   1,
		Data:   encodeTestRemoveSubscribersCommand("slot-1", 2, []string{"u2"}),
	}); err != nil {
		t.Fatalf("Apply(remove subscribers) error = %v", err)
	}

	got, _, done, err = shard.ListSubscribersPage(ctx, "slot-1", 2, "", 10)
	if err != nil {
		t.Fatalf("ListSubscribersPage() after remove error = %v", err)
	}
	if !reflect.DeepEqual(got, []string{"u1", "u3"}) {
		t.Fatalf("subscribers after remove = %#v", got)
	}
	if !done {
		t.Fatalf("done after remove = false, want true")
	}
}

func TestStateMachineApplyCreateUserAndUpsertDevice(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	result, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  1,
		Term:   1,
		Data:   EncodeCreateUserCommand(metadb.User{UID: "u1", Token: "create-token", DeviceFlag: 1, DeviceLevel: 2}),
	})
	if err != nil {
		t.Fatalf("Apply(create user) error = %v", err)
	}
	if string(result) != ApplyResultOK {
		t.Fatalf("Apply(create user) result = %q, want %q", result, ApplyResultOK)
	}

	gotUser, err := db.ForSlot(11).GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if gotUser.Token != "create-token" || gotUser.DeviceFlag != 1 || gotUser.DeviceLevel != 2 {
		t.Fatalf("created user = %#v", gotUser)
	}

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  2,
		Term:   1,
		Data:   EncodeCreateUserCommand(metadb.User{UID: "u1", Token: "overwrite-attempt", DeviceFlag: 9, DeviceLevel: 9}),
	}); err != nil {
		t.Fatalf("Apply(duplicate create user) error = %v", err)
	}

	gotUser, err = db.ForSlot(11).GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser() after duplicate create error = %v", err)
	}
	if gotUser.Token != "create-token" || gotUser.DeviceFlag != 1 || gotUser.DeviceLevel != 2 {
		t.Fatalf("user after duplicate create = %#v", gotUser)
	}

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  3,
		Term:   1,
		Data:   EncodeUpsertDeviceCommand(metadb.Device{UID: "u1", DeviceFlag: 1, Token: "app-token", DeviceLevel: 5}),
	}); err != nil {
		t.Fatalf("Apply(upsert device) error = %v", err)
	}

	gotDevice, err := db.ForSlot(11).GetDevice(ctx, "u1", 1)
	if err != nil {
		t.Fatalf("GetDevice() error = %v", err)
	}
	if gotDevice.Token != "app-token" || gotDevice.DeviceLevel != 5 {
		t.Fatalf("stored device = %#v", gotDevice)
	}
}

func TestStateMachineRejectsIncompleteChannelRuntimeMetaCommand(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	cmd := make([]byte, 0, headerSize+32)
	cmd = append(cmd, commandVersion, cmdTypeUpsertChannelRuntimeMeta)
	cmd = appendStringTLVField(cmd, tagRuntimeMetaChannelID, "partial-meta")
	cmd = appendInt64TLVField(cmd, tagRuntimeMetaChannelType, 1)

	_, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  1,
		Term:   1,
		Data:   cmd,
	})
	if !errors.Is(err, metadb.ErrCorruptValue) {
		t.Fatalf("Apply(incomplete runtime meta) err = %v, want ErrCorruptValue", err)
	}
}

func TestStateMachineRejectsIncompleteDeleteChannelRuntimeMetaCommand(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	cmd := make([]byte, 0, headerSize+32)
	cmd = append(cmd, commandVersion, cmdTypeDeleteChannelRuntimeMeta)
	cmd = appendStringTLVField(cmd, tagRuntimeMetaChannelID, "partial-delete")

	_, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  1,
		Term:   1,
		Data:   cmd,
	})
	if !errors.Is(err, metadb.ErrCorruptValue) {
		t.Fatalf("Apply(incomplete delete runtime meta) err = %v, want ErrCorruptValue", err)
	}
}

const (
	testCmdTypeAddSubscribers    uint8 = 8
	testCmdTypeRemoveSubscribers uint8 = 9

	testTagSubscriberChannelID   uint8 = 1
	testTagSubscriberChannelType uint8 = 2
	testTagSubscriberUIDs        uint8 = 3
)

func encodeTestAddSubscribersCommand(channelID string, channelType int64, uids []string) []byte {
	return encodeTestSubscriberCommand(testCmdTypeAddSubscribers, channelID, channelType, uids)
}

func encodeTestRemoveSubscribersCommand(channelID string, channelType int64, uids []string) []byte {
	return encodeTestSubscriberCommand(testCmdTypeRemoveSubscribers, channelID, channelType, uids)
}

func encodeTestSubscriberCommand(cmdType uint8, channelID string, channelType int64, uids []string) []byte {
	buf := make([]byte, 0, headerSize+len(channelID)+len(uids)*8)
	buf = append(buf, commandVersion, cmdType)
	buf = appendStringTLVField(buf, testTagSubscriberChannelID, channelID)
	buf = appendInt64TLVField(buf, testTagSubscriberChannelType, channelType)
	buf = appendBytesTLVField(buf, testTagSubscriberUIDs, []byte(strings.Join(uids, "\x00")))
	return buf
}

func TestStateMachineRejectsDeleteChannelRuntimeMetaWithEmptyChannelID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	_, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  1,
		Term:   1,
		Data:   EncodeDeleteChannelRuntimeMetaCommand("", 1),
	})
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("Apply(delete runtime meta with empty channel id) err = %v, want ErrInvalidArgument", err)
	}
}

func TestStateMachineSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  1,
		Term:   1,
		Data:   EncodeUpsertUserCommand(metadb.User{UID: "u1", Token: "t1", DeviceFlag: 1, DeviceLevel: 2}),
	}); err != nil {
		t.Fatalf("Apply(user) error = %v", err)
	}
	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  2,
		Term:   1,
		Data:   EncodeUpsertChannelCommand(metadb.Channel{ChannelID: "c1", ChannelType: 1, Ban: 1}),
	}); err != nil {
		t.Fatalf("Apply(channel) error = %v", err)
	}

	snap, err := sm.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snap.Data) == 0 {
		t.Fatal("Snapshot().Data is empty")
	}

	restoreDB := openTestDB(t)
	restoreSM := mustNewStateMachine(t, restoreDB, 11)
	if err := restoreSM.Restore(ctx, snap); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	gotUser, err := restoreDB.ForSlot(11).GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if gotUser.Token != "t1" {
		t.Fatalf("restored user = %#v", gotUser)
	}

	gotChannel, err := restoreDB.ForSlot(11).GetChannel(ctx, "c1", 1)
	if err != nil {
		t.Fatalf("GetChannel() error = %v", err)
	}
	if gotChannel.Ban != 1 {
		t.Fatalf("restored channel = %#v", gotChannel)
	}
}

func TestStateMachineSnapshotRestoreRoundTripAcrossOwnedHashSlots(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5, 7})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    1,
		Term:     1,
		Data:     EncodeUpsertUserCommand(metadb.User{UID: "u5", Token: "t5"}),
	}); err != nil {
		t.Fatalf("Apply(hashSlot=5) error = %v", err)
	}
	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 7,
		Index:    2,
		Term:     1,
		Data:     EncodeUpsertUserCommand(metadb.User{UID: "u7", Token: "t7"}),
	}); err != nil {
		t.Fatalf("Apply(hashSlot=7) error = %v", err)
	}

	snap, err := sm.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snap.Data) == 0 {
		t.Fatal("Snapshot().Data is empty")
	}

	restoreDB := openTestDB(t)
	restoreSM, err := NewStateMachineWithHashSlots(restoreDB, 11, []uint16{5, 7})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(restore) error = %v", err)
	}
	if err := restoreSM.Restore(ctx, snap); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	got5, err := restoreDB.ForHashSlot(5).GetUser(ctx, "u5")
	if err != nil {
		t.Fatalf("ForHashSlot(5).GetUser() error = %v", err)
	}
	if got5.Token != "t5" {
		t.Fatalf("restored hashSlot 5 user = %#v", got5)
	}

	got7, err := restoreDB.ForHashSlot(7).GetUser(ctx, "u7")
	if err != nil {
		t.Fatalf("ForHashSlot(7).GetUser() error = %v", err)
	}
	if got7.Token != "t7" {
		t.Fatalf("restored hashSlot 7 user = %#v", got7)
	}
}

func TestStateMachineSnapshotRestoreIncludesIncomingDeltaHashSlots(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{2})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}
	raw.UpdateIncomingDeltaHashSlots([]uint16{5})

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    1,
		Term:     1,
		Data: EncodeApplyDeltaCommand(
			21,
			9,
			5,
			EncodeUpsertUserCommand(metadb.User{UID: "u5", Token: "incoming"}),
		),
	}); err != nil {
		t.Fatalf("Apply(apply_delta hashSlot=5) error = %v", err)
	}

	snap, err := sm.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snap.Data) == 0 {
		t.Fatal("Snapshot().Data is empty")
	}

	restoreDB := openTestDB(t)
	restoreSM, err := NewStateMachineWithHashSlots(restoreDB, 11, []uint16{2})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(restore) error = %v", err)
	}
	restoreRaw, ok := restoreSM.(*stateMachine)
	if !ok {
		t.Fatalf("restore state machine type = %T, want *stateMachine", restoreSM)
	}
	restoreRaw.UpdateIncomingDeltaHashSlots([]uint16{5})

	if err := restoreSM.Restore(ctx, snap); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	got, err := restoreDB.ForHashSlot(5).GetUser(ctx, "u5")
	if err != nil {
		t.Fatalf("ForHashSlot(5).GetUser() error = %v", err)
	}
	if got.Token != "incoming" {
		t.Fatalf("restored incoming delta user = %#v", got)
	}
}

func TestStateMachineRestorePreservesImportedMigrationSourceOutboxForCleanupRecovery(t *testing.T) {
	ctx := context.Background()
	sourceDB := openTestDB(t)

	state := metadb.HashSlotMigrationState{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, LastOutboxIndex: 100}
	outbox := metadb.HashSlotMigrationOutboxRow{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, SourceIndex: 100, Data: []byte("source-cmd")}
	applied := metadb.AppliedHashSlotDelta{HashSlot: 5, SourceSlot: 11, SourceIndex: 99}
	if err := sourceDB.UpsertHashSlotMigrationState(ctx, state); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(): %v", err)
	}
	if err := sourceDB.UpsertHashSlotMigrationOutbox(ctx, outbox); err != nil {
		t.Fatalf("UpsertHashSlotMigrationOutbox(): %v", err)
	}
	if err := sourceDB.MarkAppliedHashSlotDelta(ctx, applied); err != nil {
		t.Fatalf("MarkAppliedHashSlotDelta(): %v", err)
	}
	snap, err := sourceDB.ExportHashSlotSnapshot(ctx, []uint16{5})
	if err != nil {
		t.Fatalf("ExportHashSlotSnapshot(): %v", err)
	}

	restoreDB := openTestDB(t)
	restoreSM, err := NewStateMachineWithHashSlots(restoreDB, 22, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(restore) error = %v", err)
	}
	restoreRaw, ok := restoreSM.(*stateMachine)
	if !ok {
		t.Fatalf("restore state machine type = %T, want *stateMachine", restoreSM)
	}
	if err := restoreRaw.ImportHashSlotSnapshot(ctx, snap); err != nil {
		t.Fatalf("ImportHashSlotSnapshot() error = %v", err)
	}

	gotState, err := restoreDB.LoadHashSlotMigrationState(ctx, 5)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationState() after restore: %v", err)
	}
	if gotState != state {
		t.Fatalf("restored migration state = %#v, want %#v", gotState, state)
	}
	rows, err := restoreDB.ListHashSlotMigrationOutbox(ctx, 5, 11, 22, 0, 10)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox() after restore: %v", err)
	}
	if !reflect.DeepEqual(rows, []metadb.HashSlotMigrationOutboxRow{outbox}) {
		t.Fatalf("restored source outbox rows = %#v, want %#v", rows, []metadb.HashSlotMigrationOutboxRow{outbox})
	}
	ok, err = restoreDB.HasAppliedHashSlotDelta(ctx, applied)
	if err != nil {
		t.Fatalf("HasAppliedHashSlotDelta() after restore: %v", err)
	}
	if !ok {
		t.Fatal("HasAppliedHashSlotDelta() after restore = false, want true")
	}
}

func TestStateMachineRestoreRemovesStaleLocalMigrationMeta(t *testing.T) {
	ctx := context.Background()
	sourceDB := openTestDB(t)
	if err := sourceDB.ForHashSlot(5).UpsertUser(ctx, metadb.User{UID: "u-restore-exact", Token: "snapshot-token"}); err != nil {
		t.Fatalf("source UpsertUser(): %v", err)
	}
	sourceSM, err := NewStateMachineWithHashSlots(sourceDB, 22, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(source) error = %v", err)
	}
	sourceSnap, err := sourceSM.Snapshot(ctx)
	if err != nil {
		t.Fatalf("source Snapshot(): %v", err)
	}

	restoreDB := openTestDB(t)
	staleState := metadb.HashSlotMigrationState{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, LastOutboxIndex: 200}
	staleRow := metadb.HashSlotMigrationOutboxRow{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, SourceIndex: 200, Data: []byte("stale")}
	staleDelta := metadb.AppliedHashSlotDelta{HashSlot: 5, SourceSlot: 11, SourceIndex: 199}
	if err := restoreDB.UpsertHashSlotMigrationState(ctx, staleState); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(stale): %v", err)
	}
	if err := restoreDB.UpsertHashSlotMigrationOutbox(ctx, staleRow); err != nil {
		t.Fatalf("UpsertHashSlotMigrationOutbox(stale): %v", err)
	}
	if err := restoreDB.MarkAppliedHashSlotDelta(ctx, staleDelta); err != nil {
		t.Fatalf("MarkAppliedHashSlotDelta(stale): %v", err)
	}
	restoreSM, err := NewStateMachineWithHashSlots(restoreDB, 22, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(restore) error = %v", err)
	}
	if err := restoreSM.Restore(ctx, sourceSnap); err != nil {
		t.Fatalf("Restore(): %v", err)
	}

	if _, err := restoreDB.LoadHashSlotMigrationState(ctx, 5); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("LoadHashSlotMigrationState() after restore err = %v, want ErrNotFound", err)
	}
	if _, err := restoreDB.LoadHashSlotMigrationOutbox(ctx, 5, 11, 22, 200); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("LoadHashSlotMigrationOutbox() after restore err = %v, want ErrNotFound", err)
	}
	applied, err := restoreDB.HasAppliedHashSlotDelta(ctx, staleDelta)
	if err != nil {
		t.Fatalf("HasAppliedHashSlotDelta() after restore: %v", err)
	}
	if applied {
		t.Fatal("HasAppliedHashSlotDelta() after restore = true, want false")
	}
	got, err := restoreDB.ForHashSlot(5).GetUser(ctx, "u-restore-exact")
	if err != nil {
		t.Fatalf("ForHashSlot(5).GetUser() after restore: %v", err)
	}
	if got.Token != "snapshot-token" {
		t.Fatalf("restored user = %#v", got)
	}
}

func TestStateMachineImportHashSlotSnapshotAllowsIncomingDeltaBeforeRuntimeTableUpdate(t *testing.T) {
	ctx := context.Background()
	sourceDB := openTestDB(t)
	if err := sourceDB.ForHashSlot(5).UpsertUser(ctx, metadb.User{UID: "u-snapshot", Token: "snapshot-token"}); err != nil {
		t.Fatalf("source UpsertUser(): %v", err)
	}
	snap, err := sourceDB.ExportHashSlotSnapshot(ctx, []uint16{5})
	if err != nil {
		t.Fatalf("ExportHashSlotSnapshot(): %v", err)
	}

	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 22, []uint16{7})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}
	if err := raw.ImportHashSlotSnapshot(ctx, snap); err != nil {
		t.Fatalf("ImportHashSlotSnapshot(): %v", err)
	}

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   22,
		HashSlot: 5,
		Index:    10,
		Term:     1,
		Data: EncodeApplyDeltaCommand(
			11,
			101,
			5,
			EncodeUpsertUserCommand(metadb.User{UID: "u-import-delta", Token: "token-1"}),
		),
	}); err != nil {
		t.Fatalf("Apply(apply_delta after snapshot import) error = %v", err)
	}
	got, err := db.ForHashSlot(5).GetUser(ctx, "u-import-delta")
	if err != nil {
		t.Fatalf("ForHashSlot(5).GetUser() error = %v", err)
	}
	if got.Token != "token-1" {
		t.Fatalf("stored user token = %q, want token-1", got.Token)
	}
}

func TestStateMachineImportHashSlotSnapshotDoesNotClearLocalSourceFenceOnSharedDB(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sourceSM, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(source) error = %v", err)
	}
	sourceRaw := sourceSM.(*stateMachine)
	sourceRaw.migrations[5] = migrationRuntimeState{target: 22, phase: migrationPhaseSwitching}

	if _, err := sourceRaw.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    100,
		Term:     1,
		Data:     EncodeEnterFenceCommandForTarget(5, 22),
	}); err != nil {
		t.Fatalf("Apply(enter fence) error = %v", err)
	}
	snap, err := sourceRaw.ExportHashSlotSnapshot(ctx, 5)
	if err != nil {
		t.Fatalf("ExportHashSlotSnapshot(): %v", err)
	}
	newerState := metadb.HashSlotMigrationState{
		HashSlot:        5,
		SourceSlot:      11,
		TargetSlot:      22,
		Phase:           uint8(migrationPhaseSwitching),
		FenceIndex:      100,
		LastOutboxIndex: 200,
		LastAckedIndex:  100,
	}
	newerRow := metadb.HashSlotMigrationOutboxRow{
		HashSlot:    5,
		SourceSlot:  11,
		TargetSlot:  22,
		SourceIndex: 200,
		Data:        []byte("newer-local-source-delta"),
	}
	if err := db.UpsertHashSlotMigrationState(ctx, newerState); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(newer): %v", err)
	}
	if err := db.UpsertHashSlotMigrationOutbox(ctx, newerRow); err != nil {
		t.Fatalf("UpsertHashSlotMigrationOutbox(newer): %v", err)
	}

	targetSM, err := NewStateMachineWithHashSlots(db, 22, []uint16{7})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(target) error = %v", err)
	}
	targetRaw := targetSM.(*stateMachine)
	if err := targetRaw.ImportHashSlotSnapshot(ctx, snap); err != nil {
		t.Fatalf("ImportHashSlotSnapshot(): %v", err)
	}

	gotState, err := db.LoadHashSlotMigrationState(ctx, 5)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationState() after import: %v", err)
	}
	if gotState != newerState {
		t.Fatalf("migration state after import = %#v, want newer local state %#v", gotState, newerState)
	}
	if _, err := db.LoadHashSlotMigrationOutbox(ctx, 5, 11, 22, 100); err != nil {
		t.Fatalf("LoadHashSlotMigrationOutbox() after import: %v", err)
	}
	if gotRow, err := db.LoadHashSlotMigrationOutbox(ctx, 5, 11, 22, 200); err != nil {
		t.Fatalf("LoadHashSlotMigrationOutbox(newer) after import: %v", err)
	} else if !reflect.DeepEqual(gotRow, newerRow) {
		t.Fatalf("newer outbox after import = %#v, want %#v", gotRow, newerRow)
	}
	result, err := sourceRaw.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    101,
		Term:     1,
		Data:     EncodeUpsertUserCommand(metadb.User{UID: "u-after-import", Token: "token"}),
	})
	if err != nil {
		t.Fatalf("Apply(source write after import) error = %v", err)
	}
	if string(result) != ApplyResultHashSlotFenced {
		t.Fatalf("Apply(source write after import) result = %q, want %q", result, ApplyResultHashSlotFenced)
	}
}

func TestStateMachineUpdateOwnedHashSlotsAllowsReroutedWrites(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}

	raw := sm.(*stateMachine)
	raw.UpdateOwnedHashSlots([]uint16{7})

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 7,
		Index:    1,
		Term:     1,
		Data:     EncodeUpsertUserCommand(metadb.User{UID: "u7", Token: "rerouted"}),
	}); err != nil {
		t.Fatalf("Apply(hashSlot=7) error = %v", err)
	}

	got, err := db.ForHashSlot(7).GetUser(ctx, "u7")
	if err != nil {
		t.Fatalf("ForHashSlot(7).GetUser() error = %v", err)
	}
	if got.Token != "rerouted" {
		t.Fatalf("hashSlot 7 user = %#v", got)
	}

	_, err = sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    2,
		Term:     1,
		Data:     EncodeUpsertUserCommand(metadb.User{UID: "u5", Token: "stale"}),
	})
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("Apply(hashSlot=5) err = %v, want ErrInvalidArgument", err)
	}
}

func TestStateMachineSnapshotIsSlotScoped(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  1,
		Term:   1,
		Data:   EncodeUpsertUserCommand(metadb.User{UID: "u1", Token: "slot11"}),
	}); err != nil {
		t.Fatalf("Apply(slot11) error = %v", err)
	}
	if err := db.ForSlot(12).CreateUser(ctx, metadb.User{UID: "u1", Token: "slot12"}); err != nil {
		t.Fatalf("CreateUser(slot12) error = %v", err)
	}

	snap, err := sm.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	restoreDB := openTestDB(t)
	restoreSM := mustNewStateMachine(t, restoreDB, 11)
	if err := restoreSM.Restore(ctx, snap); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	gotUser, err := restoreDB.ForSlot(11).GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser(slot11) error = %v", err)
	}
	if gotUser.Token != "slot11" {
		t.Fatalf("slot11 user = %#v", gotUser)
	}

	_, err = restoreDB.ForSlot(12).GetUser(ctx, "u1")
	if !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetUser(slot12) err = %v, want ErrNotFound", err)
	}
}

func TestStateMachineSnapshotRestoreIncludesChannelRuntimeMeta(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    "snap-meta",
		ChannelType:  8,
		ChannelEpoch: 4,
		LeaderEpoch:  2,
		Replicas:     []uint64{3, 1, 2},
		ISR:          []uint64{2, 1},
		Leader:       1,
		MinISR:       2,
		Status:       3,
		Features:     15,
		LeaseUntilMS: 1700000005678,
	}

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  1,
		Term:   1,
		Data:   EncodeUpsertChannelRuntimeMetaCommand(meta),
	}); err != nil {
		t.Fatalf("Apply(runtime meta) error = %v", err)
	}

	snap, err := sm.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	restoreDB := openTestDB(t)
	restoreSM := mustNewStateMachine(t, restoreDB, 11)
	if err := restoreSM.Restore(ctx, snap); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	got, err := restoreDB.ForSlot(11).GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}

	want := meta
	want.Replicas = []uint64{1, 2, 3}
	want.ISR = []uint64{1, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restored runtime meta = %#v, want %#v", got, want)
	}
}

func TestStateMachineRejectsMismatchedSlotID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	_, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 12,
		Index:  1,
		Term:   1,
		Data:   EncodeUpsertUserCommand(metadb.User{UID: "u1", Token: "t1"}),
	})
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("Apply() err = %v, want ErrInvalidArgument", err)
	}
}

func TestStateMachineRejectsTruncatedCommand(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	_, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  1,
		Term:   1,
		Data:   []byte{0x01}, // only version byte, missing cmdType
	})
	if !errors.Is(err, metadb.ErrCorruptValue) {
		t.Fatalf("Apply(truncated) err = %v, want ErrCorruptValue", err)
	}
}

func TestStateMachineRejectsUnknownCommand(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)

	_, err := sm.Apply(ctx, multiraft.Command{
		SlotID: 11,
		Index:  1,
		Term:   1,
		Data:   []byte{commandVersion, 0xFF}, // valid header, unknown command type
	})
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("Apply(unknown) err = %v, want ErrInvalidArgument", err)
	}
}

func TestNewStateMachineValidation(t *testing.T) {
	db := openTestDB(t)

	if _, err := NewStateMachine(nil, 1); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("NewStateMachine(nil, 1) err = %v, want ErrInvalidArgument", err)
	}
	if _, err := NewStateMachine(db, 0); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("NewStateMachine(db, 0) err = %v, want ErrInvalidArgument", err)
	}
	sm, err := NewStateMachine(db, 1)
	if err != nil {
		t.Fatalf("NewStateMachine(db, 1) error = %v", err)
	}
	if sm == nil {
		t.Fatal("NewStateMachine returned nil")
	}
}

// --- ApplyBatch tests ---

func TestApplyBatchUpsertsMultipleCommands(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	bsm := sm.(multiraft.BatchStateMachine)

	cmds := []multiraft.Command{
		{SlotID: 11, Index: 1, Term: 1, Data: EncodeUpsertUserCommand(metadb.User{UID: "u1", Token: "t1", DeviceFlag: 1, DeviceLevel: 2})},
		{SlotID: 11, Index: 2, Term: 1, Data: EncodeUpsertChannelCommand(metadb.Channel{ChannelID: "c1", ChannelType: 1, Ban: 3})},
		{SlotID: 11, Index: 3, Term: 1, Data: EncodeUpsertUserCommand(metadb.User{UID: "u2", Token: "t2", DeviceFlag: 4, DeviceLevel: 5})},
	}

	results, err := bsm.ApplyBatch(ctx, cmds)
	if err != nil {
		t.Fatalf("ApplyBatch() error = %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("ApplyBatch() returned %d results, want 3", len(results))
	}
	for i, r := range results {
		if string(r) != ApplyResultOK {
			t.Fatalf("result[%d] = %q, want %q", i, r, ApplyResultOK)
		}
	}

	shard := db.ForSlot(11)
	gotU1, err := shard.GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser(u1) error = %v", err)
	}
	if gotU1.Token != "t1" || gotU1.DeviceFlag != 1 || gotU1.DeviceLevel != 2 {
		t.Fatalf("u1 = %+v", gotU1)
	}

	gotU2, err := shard.GetUser(ctx, "u2")
	if err != nil {
		t.Fatalf("GetUser(u2) error = %v", err)
	}
	if gotU2.Token != "t2" || gotU2.DeviceFlag != 4 || gotU2.DeviceLevel != 5 {
		t.Fatalf("u2 = %+v", gotU2)
	}

	gotCh, err := shard.GetChannel(ctx, "c1", 1)
	if err != nil {
		t.Fatalf("GetChannel(c1) error = %v", err)
	}
	if gotCh.Ban != 3 {
		t.Fatalf("c1 = %+v", gotCh)
	}
}

func TestApplyBatchRejectsOnMismatchedSlotID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	bsm := sm.(multiraft.BatchStateMachine)

	cmds := []multiraft.Command{
		{SlotID: 11, Index: 1, Term: 1, Data: EncodeUpsertUserCommand(metadb.User{UID: "u1", Token: "t1"})},
		{SlotID: 99, Index: 2, Term: 1, Data: EncodeUpsertUserCommand(metadb.User{UID: "u2", Token: "t2"})},
	}

	_, err := bsm.ApplyBatch(ctx, cmds)
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("ApplyBatch() err = %v, want ErrInvalidArgument", err)
	}
}

func TestApplyBatchUsesCommandHashSlot(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	bsm, ok := sm.(multiraft.BatchStateMachine)
	if !ok {
		t.Fatalf("state machine does not implement BatchStateMachine: %T", sm)
	}

	cmds := []multiraft.Command{
		{
			SlotID:   11,
			HashSlot: 5,
			Index:    1,
			Term:     1,
			Data:     EncodeUpsertUserCommand(metadb.User{UID: "u-hs", Token: "token-hs"}),
		},
	}

	if _, err := bsm.ApplyBatch(ctx, cmds); err != nil {
		t.Fatalf("ApplyBatch() error = %v", err)
	}

	got, err := db.ForHashSlot(5).GetUser(ctx, "u-hs")
	if err != nil {
		t.Fatalf("ForHashSlot(5).GetUser() error = %v", err)
	}
	if got.Token != "token-hs" {
		t.Fatalf("stored user = %#v", got)
	}
}

func TestApplyBatchRejectsHashSlotNotOwned(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{1, 2, 3})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	bsm, ok := sm.(multiraft.BatchStateMachine)
	if !ok {
		t.Fatalf("state machine does not implement BatchStateMachine: %T", sm)
	}

	_, err = bsm.ApplyBatch(ctx, []multiraft.Command{
		{
			SlotID:   11,
			HashSlot: 5,
			Index:    1,
			Term:     1,
			Data:     EncodeUpsertUserCommand(metadb.User{UID: "u-hs", Token: "token-hs"}),
		},
	})
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("ApplyBatch() err = %v, want ErrInvalidArgument", err)
	}
}

func TestApplyBatchAcceptsApplyDeltaBeforeIncomingRuntimeMarker(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 22, []uint16{7})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	bsm, ok := sm.(multiraft.BatchStateMachine)
	if !ok {
		t.Fatalf("state machine does not implement BatchStateMachine: %T", sm)
	}

	_, err = bsm.ApplyBatch(ctx, []multiraft.Command{
		{
			SlotID:   22,
			HashSlot: 5,
			Index:    1,
			Term:     1,
			Data: EncodeApplyDeltaCommand(
				11,
				100,
				5,
				EncodeUpsertUserCommand(metadb.User{UID: "u-delta-before-marker", Token: "delta-token"}),
			),
		},
	})
	if err != nil {
		t.Fatalf("ApplyBatch(apply_delta before incoming marker) error = %v", err)
	}
	got, err := db.ForHashSlot(5).GetUser(ctx, "u-delta-before-marker")
	if err != nil {
		t.Fatalf("ForHashSlot(5).GetUser() error = %v", err)
	}
	if got.Token != "delta-token" {
		t.Fatalf("stored user = %#v", got)
	}
}

func TestApplyBatchAcceptsHashSlotZeroApplyDeltaBeforeIncomingRuntimeMarker(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 22, []uint16{7})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	bsm, ok := sm.(multiraft.BatchStateMachine)
	if !ok {
		t.Fatalf("state machine does not implement BatchStateMachine: %T", sm)
	}

	_, err = bsm.ApplyBatch(ctx, []multiraft.Command{
		{
			SlotID:   22,
			HashSlot: 0,
			Index:    1,
			Term:     1,
			Data: EncodeApplyDeltaCommand(
				11,
				100,
				0,
				EncodeUpsertUserCommand(metadb.User{UID: "u-delta-zero-before-marker", Token: "delta-token"}),
			),
		},
	})
	if err != nil {
		t.Fatalf("ApplyBatch(hash-slot 0 apply_delta before incoming marker) error = %v", err)
	}
	got, err := db.ForHashSlot(0).GetUser(ctx, "u-delta-zero-before-marker")
	if err != nil {
		t.Fatalf("ForHashSlot(0).GetUser() error = %v", err)
	}
	if got.Token != "delta-token" {
		t.Fatalf("stored user = %#v", got)
	}
}

func TestStateMachineAcceptsDelayedEnterFenceAfterOwnershipMoved(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 2, []uint16{4})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw := sm.(*stateMachine)
	raw.UpdateOwnedHashSlots([]uint16{5})

	result, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   2,
		HashSlot: 4,
		Index:    10,
		Term:     1,
		Data:     EncodeEnterFenceCommandForTarget(4, 1),
	})
	if err != nil {
		t.Fatalf("Apply(delayed enter fence) error = %v", err)
	}
	if string(result) != ApplyResultOK {
		t.Fatalf("Apply(delayed enter fence) result = %q, want %q", result, ApplyResultOK)
	}
	state, err := db.LoadHashSlotMigrationState(ctx, 4)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationState() error = %v", err)
	}
	if state.SourceSlot != 2 || state.TargetSlot != 1 || state.FenceIndex != 10 {
		t.Fatalf("migration state = %#v, want source=2 target=1 fence=10", state)
	}
}

func TestApplyBatchApplyDeltaCommandAcceptedForMigratingHashSlotBeforeFinalize(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}
	raw.migrations[5] = migrationRuntimeState{phase: migrationPhaseDelta}

	bsm, ok := sm.(multiraft.BatchStateMachine)
	if !ok {
		t.Fatalf("state machine does not implement BatchStateMachine: %T", sm)
	}

	_, err = bsm.ApplyBatch(ctx, []multiraft.Command{
		{
			SlotID:   11,
			HashSlot: 5,
			Index:    1,
			Term:     1,
			Data: EncodeApplyDeltaCommand(
				21,
				9,
				5,
				EncodeUpsertUserCommand(metadb.User{UID: "u-delta-target", Token: "delta-target"}),
			),
		},
	})
	if err != nil {
		t.Fatalf("ApplyBatch() error = %v", err)
	}

	got, err := db.ForHashSlot(5).GetUser(ctx, "u-delta-target")
	if err != nil {
		t.Fatalf("ForHashSlot(5).GetUser() error = %v", err)
	}
	if got.Token != "delta-target" {
		t.Fatalf("stored user = %#v", got)
	}
}

func TestApplyBatchApplyDeltaCommandUsesEmbeddedOriginalCommand(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	bsm, ok := sm.(multiraft.BatchStateMachine)
	if !ok {
		t.Fatalf("state machine does not implement BatchStateMachine: %T", sm)
	}

	_, err = bsm.ApplyBatch(ctx, []multiraft.Command{
		{
			SlotID:   11,
			HashSlot: 5,
			Index:    1,
			Term:     1,
			Data: EncodeApplyDeltaCommand(
				21,
				9,
				5,
				EncodeUpsertUserCommand(metadb.User{UID: "u-delta", Token: "delta-token"}),
			),
		},
	})
	if err != nil {
		t.Fatalf("ApplyBatch() error = %v", err)
	}

	got, err := db.ForHashSlot(5).GetUser(ctx, "u-delta")
	if err != nil {
		t.Fatalf("ForHashSlot(5).GetUser() error = %v", err)
	}
	if got.Token != "delta-token" {
		t.Fatalf("stored user = %#v", got)
	}
}

func TestStateMachineAcceptsApplyDeltaForMigratingHashSlotBeforeFinalize(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{2})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}
	raw.migrations[5] = migrationRuntimeState{
		phase: migrationPhaseDelta,
	}

	bsm, ok := sm.(multiraft.BatchStateMachine)
	if !ok {
		t.Fatalf("state machine does not implement BatchStateMachine: %T", sm)
	}

	_, err = bsm.ApplyBatch(ctx, []multiraft.Command{
		{
			SlotID:   11,
			HashSlot: 5,
			Index:    1,
			Term:     1,
			Data: EncodeApplyDeltaCommand(
				21,
				9,
				5,
				EncodeUpsertUserCommand(metadb.User{UID: "u-migrating", Token: "delta-token"}),
			),
		},
	})
	if err != nil {
		t.Fatalf("ApplyBatch() error = %v", err)
	}

	got, err := db.ForHashSlot(5).GetUser(ctx, "u-migrating")
	if err != nil {
		t.Fatalf("ForHashSlot(5).GetUser() error = %v", err)
	}
	if got.Token != "delta-token" {
		t.Fatalf("stored user = %#v", got)
	}
}

func TestStateMachineDeduplicatesRepeatedApplyDelta(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{2})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}
	raw.UpdateIncomingDeltaHashSlots([]uint16{5})

	cmd := multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    1,
		Term:     1,
		Data: EncodeApplyDeltaCommand(
			21,
			9,
			5,
			EncodeUpsertUserCommand(metadb.User{UID: "u-dedup", Token: "delta-token"}),
		),
	}

	if _, err := sm.Apply(ctx, cmd); err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	if _, err := sm.Apply(ctx, cmd); err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}

	got, err := db.ForHashSlot(5).GetUser(ctx, "u-dedup")
	if err != nil {
		t.Fatalf("ForHashSlot(5).GetUser() error = %v", err)
	}
	if got.Token != "delta-token" {
		t.Fatalf("stored user = %#v", got)
	}
	if len(raw.appliedDelta) != 1 {
		t.Fatalf("len(appliedDelta) = %d, want 1", len(raw.appliedDelta))
	}
}

func TestStateMachineApplyDeltaRestartDedupPersists(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	firstSM, err := NewStateMachineWithHashSlots(db, 11, []uint16{2})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(first) error = %v", err)
	}
	firstRaw, ok := firstSM.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", firstSM)
	}
	firstRaw.UpdateIncomingDeltaHashSlots([]uint16{5})

	delta := metadb.AppliedHashSlotDelta{HashSlot: 5, SourceSlot: 21, SourceIndex: 9}
	firstCmd := multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    1,
		Term:     1,
		Data: EncodeApplyDeltaCommand(
			multiraft.SlotID(delta.SourceSlot),
			delta.SourceIndex,
			delta.HashSlot,
			EncodeUpsertUserCommand(metadb.User{UID: "u-dedup-restart", Token: "first-token"}),
		),
	}
	if _, err := firstSM.Apply(ctx, firstCmd); err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	ok, err = db.HasAppliedHashSlotDelta(ctx, delta)
	if err != nil {
		t.Fatalf("HasAppliedHashSlotDelta() after first apply: %v", err)
	}
	if !ok {
		t.Fatal("HasAppliedHashSlotDelta() after first apply = false, want true")
	}

	restartedSM, err := NewStateMachineWithHashSlots(db, 11, []uint16{2})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots(restarted) error = %v", err)
	}
	restartedRaw, ok := restartedSM.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", restartedSM)
	}
	restartedRaw.UpdateIncomingDeltaHashSlots([]uint16{5})

	duplicateCmd := firstCmd
	duplicateCmd.Index = 2
	duplicateCmd.Data = EncodeApplyDeltaCommand(
		multiraft.SlotID(delta.SourceSlot),
		delta.SourceIndex,
		delta.HashSlot,
		EncodeUpsertUserCommand(metadb.User{UID: "u-dedup-restart", Token: "second-token"}),
	)
	if _, err := restartedSM.Apply(ctx, duplicateCmd); err != nil {
		t.Fatalf("duplicate Apply() after restart error = %v", err)
	}

	got, err := db.ForHashSlot(5).GetUser(ctx, "u-dedup-restart")
	if err != nil {
		t.Fatalf("ForHashSlot(5).GetUser() error = %v", err)
	}
	if got.Token != "first-token" {
		t.Fatalf("stored token after duplicate apply = %q, want %q", got.Token, "first-token")
	}
}

func TestStateMachineDeduplicatesRepeatedApplyDeltaWithinBatch(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{2})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}
	raw.UpdateIncomingDeltaHashSlots([]uint16{5})
	bsm, ok := sm.(multiraft.BatchStateMachine)
	if !ok {
		t.Fatalf("state machine does not implement BatchStateMachine: %T", sm)
	}

	delta := metadb.AppliedHashSlotDelta{HashSlot: 5, SourceSlot: 21, SourceIndex: 9}
	cmds := []multiraft.Command{
		{
			SlotID:   11,
			HashSlot: delta.HashSlot,
			Index:    1,
			Term:     1,
			Data: EncodeApplyDeltaCommand(
				multiraft.SlotID(delta.SourceSlot),
				delta.SourceIndex,
				delta.HashSlot,
				EncodeUpsertUserCommand(metadb.User{UID: "u-dedup-batch", Token: "first-token"}),
			),
		},
		{
			SlotID:   11,
			HashSlot: delta.HashSlot,
			Index:    2,
			Term:     1,
			Data: EncodeApplyDeltaCommand(
				multiraft.SlotID(delta.SourceSlot),
				delta.SourceIndex,
				delta.HashSlot,
				EncodeUpsertUserCommand(metadb.User{UID: "u-dedup-batch", Token: "second-token"}),
			),
		},
	}
	if _, err := bsm.ApplyBatch(ctx, cmds); err != nil {
		t.Fatalf("ApplyBatch() error = %v", err)
	}

	got, err := db.ForHashSlot(5).GetUser(ctx, "u-dedup-batch")
	if err != nil {
		t.Fatalf("ForHashSlot(5).GetUser() error = %v", err)
	}
	if got.Token != "first-token" {
		t.Fatalf("stored token after duplicate batch apply = %q, want %q", got.Token, "first-token")
	}
	ok, err = db.HasAppliedHashSlotDelta(ctx, delta)
	if err != nil {
		t.Fatalf("HasAppliedHashSlotDelta() after batch apply: %v", err)
	}
	if !ok {
		t.Fatal("HasAppliedHashSlotDelta() after batch apply = false, want true")
	}
	if len(raw.appliedDelta) != 1 {
		t.Fatalf("len(appliedDelta) = %d, want 1", len(raw.appliedDelta))
	}
}

func TestStateMachineForwardsDeltaDuringMigration(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}

	var (
		gotTarget multiraft.SlotID
		gotCmd    multiraft.Command
		calls     int
	)
	raw.migrations[5] = migrationRuntimeState{
		target: 22,
		phase:  migrationPhaseDelta,
	}
	raw.forwardDelta = func(_ context.Context, target multiraft.SlotID, cmd multiraft.Command) error {
		gotTarget = target
		gotCmd = cmd
		calls++
		return nil
	}

	bsm, ok := sm.(multiraft.BatchStateMachine)
	if !ok {
		t.Fatalf("state machine does not implement BatchStateMachine: %T", sm)
	}

	cmd := multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    3,
		Term:     1,
		Data:     EncodeUpsertUserCommand(metadb.User{UID: "u-forward", Token: "forward-token"}),
	}
	if _, err := bsm.ApplyBatch(ctx, []multiraft.Command{cmd}); err != nil {
		t.Fatalf("ApplyBatch() error = %v", err)
	}

	if calls != 1 {
		t.Fatalf("forwardDelta calls = %d, want 1", calls)
	}
	if gotTarget != 22 {
		t.Fatalf("forwardDelta target = %d, want 22", gotTarget)
	}
	if gotCmd.HashSlot != 5 || gotCmd.Index != 3 || gotCmd.Term != 1 || !reflect.DeepEqual(gotCmd.Data, cmd.Data) {
		t.Fatalf("forwardDelta cmd = %#v, want %#v", gotCmd, cmd)
	}
}

func TestStateMachinePersistsMigrationDeltaOutboxWithoutForwarder(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}
	raw.migrations[5] = migrationRuntimeState{target: 22, phase: migrationPhaseDelta}

	cmd := multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    100,
		Term:     1,
		Data:     EncodeUpsertUserCommand(metadb.User{UID: "u-outbox-missing-forwarder", Token: "token-1"}),
	}
	if _, err := sm.Apply(ctx, cmd); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	gotUser, err := db.ForHashSlot(5).GetUser(ctx, "u-outbox-missing-forwarder")
	if err != nil {
		t.Fatalf("ForHashSlot(5).GetUser() error = %v", err)
	}
	if gotUser.Token != "token-1" {
		t.Fatalf("stored user token = %q, want token-1", gotUser.Token)
	}
	rows, err := db.ListHashSlotMigrationOutbox(ctx, 5, 11, 22, 0, 10)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(): %v", err)
	}
	wantRow := metadb.HashSlotMigrationOutboxRow{
		HashSlot:    5,
		SourceSlot:  11,
		TargetSlot:  22,
		SourceIndex: 100,
		Data:        cmd.Data,
	}
	if !reflect.DeepEqual(rows, []metadb.HashSlotMigrationOutboxRow{wantRow}) {
		t.Fatalf("outbox rows = %#v, want %#v", rows, []metadb.HashSlotMigrationOutboxRow{wantRow})
	}
	state, err := db.LoadHashSlotMigrationState(ctx, 5)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationState(): %v", err)
	}
	if state.SourceSlot != 11 || state.TargetSlot != 22 || state.LastOutboxIndex != 100 {
		t.Fatalf("migration state = %#v, want source=11 target=22 lastOutbox=100", state)
	}
}

func TestStateMachinePersistsMigrationDeltaOutboxDuringSwitching(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}
	raw.migrations[5] = migrationRuntimeState{target: 22, phase: migrationPhaseSwitching}

	cmd := multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    100,
		Term:     1,
		Data:     EncodeUpsertUserCommand(metadb.User{UID: "u-outbox-switching", Token: "token-1"}),
	}
	if _, err := sm.Apply(ctx, cmd); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	rows, err := db.ListHashSlotMigrationOutbox(ctx, 5, 11, 22, 0, 10)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(): %v", err)
	}
	wantRow := metadb.HashSlotMigrationOutboxRow{
		HashSlot:    5,
		SourceSlot:  11,
		TargetSlot:  22,
		SourceIndex: 100,
		Data:        cmd.Data,
	}
	if !reflect.DeepEqual(rows, []metadb.HashSlotMigrationOutboxRow{wantRow}) {
		t.Fatalf("outbox rows = %#v, want %#v", rows, []metadb.HashSlotMigrationOutboxRow{wantRow})
	}
}

func TestStateMachineEnterFenceRejectsLaterSourceWrites(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}
	raw.migrations[5] = migrationRuntimeState{target: 22, phase: migrationPhaseSwitching}

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    100,
		Term:     1,
		Data:     EncodeUpsertUserCommand(metadb.User{UID: "u-before-fence", Token: "token-1"}),
	}); err != nil {
		t.Fatalf("Apply(before fence) error = %v", err)
	}
	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    101,
		Term:     1,
		Data:     EncodeEnterFenceCommand(5),
	}); err != nil {
		t.Fatalf("Apply(enter fence) error = %v", err)
	}
	result, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    102,
		Term:     1,
		Data:     EncodeUpsertUserCommand(metadb.User{UID: "u-after-fence", Token: "token-2"}),
	})
	if err != nil {
		t.Fatalf("Apply(after fence) error = %v", err)
	}
	if string(result) != ApplyResultHashSlotFenced {
		t.Fatalf("Apply(after fence) result = %q, want %q", result, ApplyResultHashSlotFenced)
	}
	if _, err := db.ForHashSlot(5).GetUser(ctx, "u-after-fence"); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetUser(after fence) err = %v, want ErrNotFound", err)
	}

	state, err := db.LoadHashSlotMigrationState(ctx, 5)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationState(): %v", err)
	}
	if state.FenceIndex != 101 || state.LastOutboxIndex != 101 {
		t.Fatalf("migration state = %#v, want fence=101 lastOutbox=101", state)
	}
	rows, err := db.ListHashSlotMigrationOutbox(ctx, 5, 11, 22, 0, 10)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(): %v", err)
	}
	if len(rows) != 2 || rows[0].SourceIndex != 100 || rows[1].SourceIndex != 101 {
		t.Fatalf("outbox rows = %#v, want source indexes 100 and 101", rows)
	}
}

func TestStateMachineEnterSnapshotFenceDoesNotForwardBeforeTargetAcceptsDelta(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}
	forwarded := 0
	raw.forwardDelta = func(context.Context, multiraft.SlotID, multiraft.Command) error {
		forwarded++
		return nil
	}

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    101,
		Term:     1,
		Data:     EncodeEnterFenceCommandForTarget(5, 22),
	}); err != nil {
		t.Fatalf("Apply(snapshot fence) error = %v", err)
	}
	if forwarded != 0 {
		t.Fatalf("forwarded snapshot fence deltas = %d, want 0 before target is marked incoming", forwarded)
	}
	rows, err := db.ListHashSlotMigrationOutbox(ctx, 5, 11, 22, 0, 10)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(): %v", err)
	}
	if len(rows) != 1 || rows[0].SourceIndex != 101 {
		t.Fatalf("outbox rows = %#v, want persisted fence source index 101", rows)
	}
}

func TestStateMachineAllowsApplyDeltaAfterEnterFence(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}
	raw.migrations[5] = migrationRuntimeState{target: 22, phase: migrationPhaseSwitching}
	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    101,
		Term:     1,
		Data:     EncodeEnterFenceCommand(5),
	}); err != nil {
		t.Fatalf("Apply(enter fence) error = %v", err)
	}

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    102,
		Term:     1,
		Data: EncodeApplyDeltaCommand(
			21,
			9,
			5,
			EncodeUpsertUserCommand(metadb.User{UID: "u-delta-after-fence", Token: "delta-token"}),
		),
	}); err != nil {
		t.Fatalf("Apply(apply_delta after fence) error = %v", err)
	}

	got, err := db.ForHashSlot(5).GetUser(ctx, "u-delta-after-fence")
	if err != nil {
		t.Fatalf("ForHashSlot(5).GetUser() error = %v", err)
	}
	if got.Token != "delta-token" {
		t.Fatalf("stored user token = %q, want delta-token", got.Token)
	}
}

func TestStateMachineDurableFenceRejectsWritesWithoutRuntimeMigration(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if err := db.UpsertHashSlotMigrationState(ctx, metadb.HashSlotMigrationState{
		HashSlot:        5,
		SourceSlot:      11,
		TargetSlot:      22,
		Phase:           uint8(migrationPhaseSwitching),
		FenceIndex:      101,
		LastOutboxIndex: 101,
		LastAckedIndex:  101,
	}); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(): %v", err)
	}

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}

	result, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    102,
		Term:     1,
		Data:     EncodeUpsertUserCommand(metadb.User{UID: "u-durable-fence", Token: "token-1"}),
	})
	if err != nil {
		t.Fatalf("Apply(after durable fence) error = %v", err)
	}
	if string(result) != ApplyResultHashSlotFenced {
		t.Fatalf("Apply(after durable fence) result = %q, want %q", result, ApplyResultHashSlotFenced)
	}
	if _, err := db.ForHashSlot(5).GetUser(ctx, "u-durable-fence"); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetUser(after durable fence) err = %v, want ErrNotFound", err)
	}
}

func TestStateMachinePersistsMigrationDeltaOutboxWhenForwarderFails(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}
	raw.migrations[5] = migrationRuntimeState{target: 22, phase: migrationPhaseDelta}
	raw.forwardDelta = func(context.Context, multiraft.SlotID, multiraft.Command) error {
		return errors.New("forward unavailable")
	}

	cmd := multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    101,
		Term:     1,
		Data:     EncodeUpsertUserCommand(metadb.User{UID: "u-outbox-forward-fails", Token: "token-1"}),
	}
	if _, err := sm.Apply(ctx, cmd); err != nil {
		t.Fatalf("Apply() error = %v, want local commit with durable outbox", err)
	}

	rows, err := db.ListHashSlotMigrationOutbox(ctx, 5, 11, 22, 0, 10)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(): %v", err)
	}
	if len(rows) != 1 || rows[0].SourceIndex != 101 {
		t.Fatalf("outbox rows = %#v, want source index 101", rows)
	}
	gotUser, err := db.ForHashSlot(5).GetUser(ctx, "u-outbox-forward-fails")
	if err != nil {
		t.Fatalf("ForHashSlot(5).GetUser() error = %v", err)
	}
	if gotUser.Token != "token-1" {
		t.Fatalf("stored user token = %q, want token-1", gotUser.Token)
	}
}

func TestStateMachineAckHashSlotMigrationOutboxAdvancesProgressAndDeletesRow(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}

	state := metadb.HashSlotMigrationState{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, LastOutboxIndex: 100}
	row := metadb.HashSlotMigrationOutboxRow{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, SourceIndex: 100, Data: []byte("cmd")}
	if err := db.UpsertHashSlotMigrationState(ctx, state); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(): %v", err)
	}
	if err := db.UpsertHashSlotMigrationOutbox(ctx, row); err != nil {
		t.Fatalf("UpsertHashSlotMigrationOutbox(): %v", err)
	}

	if err := raw.AckHashSlotMigrationOutbox(ctx, 5, 11, 22, 100); err != nil {
		t.Fatalf("AckHashSlotMigrationOutbox(): %v", err)
	}
	gotState, err := db.LoadHashSlotMigrationState(ctx, 5)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationState(): %v", err)
	}
	if gotState.LastAckedIndex != 100 {
		t.Fatalf("LastAckedIndex = %d, want 100", gotState.LastAckedIndex)
	}
	if _, err := db.LoadHashSlotMigrationOutbox(ctx, 5, 11, 22, 100); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("LoadHashSlotMigrationOutbox() after ack err = %v, want ErrNotFound", err)
	}

	if err := raw.AckHashSlotMigrationOutbox(ctx, 5, 11, 22, 101); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("AckHashSlotMigrationOutbox(beyond outbox) err = %v, want ErrInvalidArgument", err)
	}
}

func TestStateMachineApplyReplicatedAckHashSlotMigrationOutboxAdvancesProgressAndDeletesRow(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}

	state := metadb.HashSlotMigrationState{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, LastOutboxIndex: 100}
	row := metadb.HashSlotMigrationOutboxRow{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, SourceIndex: 100, Data: []byte("cmd")}
	if err := db.UpsertHashSlotMigrationState(ctx, state); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(): %v", err)
	}
	if err := db.UpsertHashSlotMigrationOutbox(ctx, row); err != nil {
		t.Fatalf("UpsertHashSlotMigrationOutbox(): %v", err)
	}

	result, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    101,
		Term:     1,
		Data:     EncodeAckHashSlotMigrationOutboxCommand(5, 11, 22, 100),
	})
	if err != nil {
		t.Fatalf("Apply(replicated ack) error = %v", err)
	}
	if string(result) != ApplyResultOK {
		t.Fatalf("Apply(replicated ack) result = %q, want %q", result, ApplyResultOK)
	}
	gotState, err := db.LoadHashSlotMigrationState(ctx, 5)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationState(): %v", err)
	}
	if gotState.LastAckedIndex != 100 {
		t.Fatalf("LastAckedIndex = %d, want 100", gotState.LastAckedIndex)
	}
	if _, err := db.LoadHashSlotMigrationOutbox(ctx, 5, 11, 22, 100); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("LoadHashSlotMigrationOutbox() after replicated ack err = %v, want ErrNotFound", err)
	}
}

func TestStateMachineCleanupHashSlotMigrationOutboxDeletesStateAndRows(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}

	state := metadb.HashSlotMigrationState{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, LastOutboxIndex: 101}
	rows := []metadb.HashSlotMigrationOutboxRow{
		{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, SourceIndex: 100, Data: []byte("cmd-100")},
		{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, SourceIndex: 101, Data: []byte("cmd-101")},
		{HashSlot: 5, SourceSlot: 11, TargetSlot: 33, SourceIndex: 100, Data: []byte("other-target")},
	}
	if err := db.UpsertHashSlotMigrationState(ctx, state); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(): %v", err)
	}
	for _, row := range rows {
		if err := db.UpsertHashSlotMigrationOutbox(ctx, row); err != nil {
			t.Fatalf("UpsertHashSlotMigrationOutbox(%#v): %v", row, err)
		}
	}

	if err := raw.CleanupHashSlotMigrationOutbox(ctx, 5, 11, 22, 101); err != nil {
		t.Fatalf("CleanupHashSlotMigrationOutbox(): %v", err)
	}
	if _, err := db.LoadHashSlotMigrationState(ctx, 5); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("LoadHashSlotMigrationState() after cleanup err = %v, want ErrNotFound", err)
	}
	gotRows, err := db.ListHashSlotMigrationOutbox(ctx, 5, 11, 22, 0, 10)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(cleaned pair): %v", err)
	}
	if len(gotRows) != 0 {
		t.Fatalf("cleaned pair rows = %#v, want none", gotRows)
	}
	otherRows, err := db.ListHashSlotMigrationOutbox(ctx, 5, 11, 33, 0, 10)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(other target): %v", err)
	}
	if len(otherRows) != 1 || otherRows[0].TargetSlot != 33 {
		t.Fatalf("other target rows = %#v, want preserved row", otherRows)
	}
}

func TestStateMachineApplyReplicatedCleanupHashSlotMigrationOutboxDeletesStateAndRows(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}

	state := metadb.HashSlotMigrationState{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, LastOutboxIndex: 101}
	rows := []metadb.HashSlotMigrationOutboxRow{
		{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, SourceIndex: 100, Data: []byte("cmd-100")},
		{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, SourceIndex: 101, Data: []byte("cmd-101")},
		{HashSlot: 5, SourceSlot: 11, TargetSlot: 33, SourceIndex: 100, Data: []byte("other-target")},
	}
	if err := db.UpsertHashSlotMigrationState(ctx, state); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(): %v", err)
	}
	for _, row := range rows {
		if err := db.UpsertHashSlotMigrationOutbox(ctx, row); err != nil {
			t.Fatalf("UpsertHashSlotMigrationOutbox(%#v): %v", row, err)
		}
	}

	result, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    102,
		Term:     1,
		Data:     EncodeCleanupHashSlotMigrationOutboxCommand(5, 11, 22, 101),
	})
	if err != nil {
		t.Fatalf("Apply(replicated cleanup) error = %v", err)
	}
	if string(result) != ApplyResultOK {
		t.Fatalf("Apply(replicated cleanup) result = %q, want %q", result, ApplyResultOK)
	}
	if _, err := db.LoadHashSlotMigrationState(ctx, 5); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("LoadHashSlotMigrationState() after replicated cleanup err = %v, want ErrNotFound", err)
	}
	gotRows, err := db.ListHashSlotMigrationOutbox(ctx, 5, 11, 22, 0, 10)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(cleaned pair): %v", err)
	}
	if len(gotRows) != 0 {
		t.Fatalf("cleaned pair rows = %#v, want none", gotRows)
	}
	otherRows, err := db.ListHashSlotMigrationOutbox(ctx, 5, 11, 33, 0, 10)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(other target): %v", err)
	}
	if len(otherRows) != 1 || otherRows[0].TargetSlot != 33 {
		t.Fatalf("other target rows = %#v, want preserved row", otherRows)
	}
}

func TestStateMachineApplyStaleReplicatedCleanupDoesNotDeleteNewerSameDirectionState(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}

	state := metadb.HashSlotMigrationState{
		HashSlot:        5,
		SourceSlot:      11,
		TargetSlot:      22,
		Phase:           uint8(migrationPhaseSwitching),
		FenceIndex:      200,
		LastOutboxIndex: 200,
	}
	rows := []metadb.HashSlotMigrationOutboxRow{
		{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, SourceIndex: 100, Data: []byte("old-cmd")},
		{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, SourceIndex: 200, Data: []byte("new-cmd")},
	}
	if err := db.UpsertHashSlotMigrationState(ctx, state); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(): %v", err)
	}
	for _, row := range rows {
		if err := db.UpsertHashSlotMigrationOutbox(ctx, row); err != nil {
			t.Fatalf("UpsertHashSlotMigrationOutbox(%#v): %v", row, err)
		}
	}

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    201,
		Term:     1,
		Data:     EncodeCleanupHashSlotMigrationOutboxCommand(5, 11, 22, 100),
	}); err != nil {
		t.Fatalf("Apply(stale replicated cleanup) error = %v", err)
	}

	gotState, err := db.LoadHashSlotMigrationState(ctx, 5)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationState(): %v", err)
	}
	if gotState.LastOutboxIndex != 200 || gotState.FenceIndex != 200 {
		t.Fatalf("migration state after stale cleanup = %#v, want newer state preserved", gotState)
	}
	if _, err := db.LoadHashSlotMigrationOutbox(ctx, 5, 11, 22, 100); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("old outbox after stale cleanup err = %v, want ErrNotFound", err)
	}
	if _, err := db.LoadHashSlotMigrationOutbox(ctx, 5, 11, 22, 200); err != nil {
		t.Fatalf("new outbox after stale cleanup err = %v, want preserved row", err)
	}
}

func TestStateMachineApplyStaleReplicatedCleanupDoesNotDeleteUnindexedSameDirectionState(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}

	state := metadb.HashSlotMigrationState{
		HashSlot:   5,
		SourceSlot: 11,
		TargetSlot: 22,
		Phase:      uint8(migrationPhaseDelta),
	}
	if err := db.UpsertHashSlotMigrationState(ctx, state); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(): %v", err)
	}

	if _, err := sm.Apply(ctx, multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    201,
		Term:     1,
		Data:     EncodeCleanupHashSlotMigrationOutboxCommand(5, 11, 22, 100),
	}); err != nil {
		t.Fatalf("Apply(stale replicated cleanup) error = %v", err)
	}

	gotState, err := db.LoadHashSlotMigrationState(ctx, 5)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationState(): %v", err)
	}
	if gotState.LastOutboxIndex != 0 || gotState.SourceSlot != 11 || gotState.TargetSlot != 22 {
		t.Fatalf("migration state after stale cleanup = %#v, want unindexed state preserved", gotState)
	}
}

func TestStateMachineDirectCleanupDoesNotDeleteRowsBeyondCurrentState(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw := sm.(*stateMachine)

	state := metadb.HashSlotMigrationState{
		HashSlot:        5,
		SourceSlot:      11,
		TargetSlot:      22,
		Phase:           uint8(migrationPhaseSwitching),
		FenceIndex:      200,
		LastOutboxIndex: 200,
	}
	rows := []metadb.HashSlotMigrationOutboxRow{
		{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, SourceIndex: 100, Data: []byte("old-cmd")},
		{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, SourceIndex: 200, Data: []byte("new-cmd")},
	}
	if err := db.UpsertHashSlotMigrationState(ctx, state); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(): %v", err)
	}
	for _, row := range rows {
		if err := db.UpsertHashSlotMigrationOutbox(ctx, row); err != nil {
			t.Fatalf("UpsertHashSlotMigrationOutbox(%#v): %v", row, err)
		}
	}

	if err := raw.CleanupHashSlotMigrationOutbox(ctx, 5, 11, 22, 100); err != nil {
		t.Fatalf("CleanupHashSlotMigrationOutbox(): %v", err)
	}

	if _, err := db.LoadHashSlotMigrationState(ctx, 5); err != nil {
		t.Fatalf("LoadHashSlotMigrationState() after direct cleanup: %v", err)
	}
	if _, err := db.LoadHashSlotMigrationOutbox(ctx, 5, 11, 22, 100); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("old outbox after direct cleanup err = %v, want ErrNotFound", err)
	}
	if _, err := db.LoadHashSlotMigrationOutbox(ctx, 5, 11, 22, 200); err != nil {
		t.Fatalf("new outbox after direct cleanup err = %v, want preserved row", err)
	}
}

func TestStateMachineReplacesStaleMigrationOutboxStateForNewTarget(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	staleState := metadb.HashSlotMigrationState{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, LastOutboxIndex: 100}
	staleRow := metadb.HashSlotMigrationOutboxRow{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, SourceIndex: 100, Data: []byte("stale-cmd")}
	if err := db.UpsertHashSlotMigrationState(ctx, staleState); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(stale): %v", err)
	}
	if err := db.UpsertHashSlotMigrationOutbox(ctx, staleRow); err != nil {
		t.Fatalf("UpsertHashSlotMigrationOutbox(stale): %v", err)
	}

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}
	raw.migrations[5] = migrationRuntimeState{target: 33, phase: migrationPhaseDelta}

	cmd := multiraft.Command{
		SlotID:   11,
		HashSlot: 5,
		Index:    101,
		Term:     1,
		Data:     EncodeUpsertUserCommand(metadb.User{UID: "u-new-target", Token: "token-new"}),
	}
	if _, err := sm.Apply(ctx, cmd); err != nil {
		t.Fatalf("Apply() with stale migration state error = %v", err)
	}

	staleRows, err := db.ListHashSlotMigrationOutbox(ctx, 5, 11, 22, 0, 10)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(stale target): %v", err)
	}
	if len(staleRows) != 0 {
		t.Fatalf("stale target rows = %#v, want none", staleRows)
	}
	newRows, err := db.ListHashSlotMigrationOutbox(ctx, 5, 11, 33, 0, 10)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(new target): %v", err)
	}
	if len(newRows) != 1 || newRows[0].SourceIndex != 101 {
		t.Fatalf("new target rows = %#v, want source index 101", newRows)
	}
	state, err := db.LoadHashSlotMigrationState(ctx, 5)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationState(): %v", err)
	}
	if state.TargetSlot != 33 || state.LastOutboxIndex != 101 {
		t.Fatalf("migration state = %#v, want target 33 lastOutbox 101", state)
	}
}

func TestStateMachineReplacesStaleMigrationOutboxStateOncePerBatch(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	staleState := metadb.HashSlotMigrationState{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, LastOutboxIndex: 100}
	staleRow := metadb.HashSlotMigrationOutboxRow{HashSlot: 5, SourceSlot: 11, TargetSlot: 22, SourceIndex: 100, Data: []byte("stale-cmd")}
	if err := db.UpsertHashSlotMigrationState(ctx, staleState); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(stale): %v", err)
	}
	if err := db.UpsertHashSlotMigrationOutbox(ctx, staleRow); err != nil {
		t.Fatalf("UpsertHashSlotMigrationOutbox(stale): %v", err)
	}

	sm, err := NewStateMachineWithHashSlots(db, 11, []uint16{5})
	if err != nil {
		t.Fatalf("NewStateMachineWithHashSlots() error = %v", err)
	}
	raw, ok := sm.(*stateMachine)
	if !ok {
		t.Fatalf("state machine type = %T, want *stateMachine", sm)
	}
	raw.migrations[5] = migrationRuntimeState{target: 33, phase: migrationPhaseDelta}
	bsm, ok := sm.(multiraft.BatchStateMachine)
	if !ok {
		t.Fatalf("state machine does not implement BatchStateMachine: %T", sm)
	}

	cmds := []multiraft.Command{
		{
			SlotID:   11,
			HashSlot: 5,
			Index:    101,
			Term:     1,
			Data:     EncodeUpsertUserCommand(metadb.User{UID: "u-new-target-1", Token: "token-1"}),
		},
		{
			SlotID:   11,
			HashSlot: 5,
			Index:    102,
			Term:     1,
			Data:     EncodeUpsertUserCommand(metadb.User{UID: "u-new-target-2", Token: "token-2"}),
		},
	}
	if _, err := bsm.ApplyBatch(ctx, cmds); err != nil {
		t.Fatalf("ApplyBatch() with stale migration state error = %v", err)
	}

	rows, err := db.ListHashSlotMigrationOutbox(ctx, 5, 11, 33, 0, 10)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(new target): %v", err)
	}
	if len(rows) != 2 || rows[0].SourceIndex != 101 || rows[1].SourceIndex != 102 {
		t.Fatalf("new target rows = %#v, want source indexes 101 and 102", rows)
	}
}

func TestApplyBatchAtomicity(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	bsm := sm.(multiraft.BatchStateMachine)

	// First command is valid, second has invalid data — the whole batch should fail.
	cmds := []multiraft.Command{
		{SlotID: 11, Index: 1, Term: 1, Data: EncodeUpsertUserCommand(metadb.User{UID: "u-atomic", Token: "t1"})},
		{SlotID: 11, Index: 2, Term: 1, Data: []byte{commandVersion, 0xFF}}, // unknown type
	}

	_, err := bsm.ApplyBatch(ctx, cmds)
	if err == nil {
		t.Fatal("ApplyBatch() expected error for invalid command")
	}

	// Verify no partial writes: u-atomic should not exist.
	_, err = db.ForSlot(11).GetUser(ctx, "u-atomic")
	if !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetUser(u-atomic) err = %v, want ErrNotFound (no partial writes)", err)
	}
}

func TestApplyBatchCreateUserPreservesFirstWriteWithinBatch(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	bsm, ok := mustNewStateMachine(t, db, 11).(multiraft.BatchStateMachine)
	if !ok {
		t.Fatal("state machine does not implement multiraft.BatchStateMachine")
	}

	cmds := []multiraft.Command{
		{SlotID: 11, Index: 1, Term: 1, Data: EncodeCreateUserCommand(metadb.User{UID: "u-atomic", Token: "first"})},
		{SlotID: 11, Index: 2, Term: 1, Data: EncodeCreateUserCommand(metadb.User{UID: "u-atomic", Token: "second"})},
	}

	results, err := bsm.ApplyBatch(ctx, cmds)
	if err != nil {
		t.Fatalf("ApplyBatch() error = %v", err)
	}
	if len(results) != 2 || string(results[0]) != ApplyResultOK || string(results[1]) != ApplyResultOK {
		t.Fatalf("ApplyBatch() results = %q", results)
	}

	got, err := db.ForSlot(11).GetUser(ctx, "u-atomic")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got.Token != "first" {
		t.Fatalf("stored user = %#v", got)
	}
}

func TestApplyBatchCreateUserDoesNotOverwritePriorUpsertWithinBatch(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	bsm, ok := mustNewStateMachine(t, db, 11).(multiraft.BatchStateMachine)
	if !ok {
		t.Fatal("state machine does not implement multiraft.BatchStateMachine")
	}

	cmds := []multiraft.Command{
		{SlotID: 11, Index: 1, Term: 1, Data: EncodeUpsertUserCommand(metadb.User{UID: "u-upsert", Token: "upserted", DeviceFlag: 7, DeviceLevel: 8})},
		{SlotID: 11, Index: 2, Term: 1, Data: EncodeCreateUserCommand(metadb.User{UID: "u-upsert", Token: "created", DeviceFlag: 1, DeviceLevel: 2})},
	}

	_, err := bsm.ApplyBatch(ctx, cmds)
	if err != nil {
		t.Fatalf("ApplyBatch() error = %v", err)
	}

	got, err := db.ForSlot(11).GetUser(ctx, "u-upsert")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got.Token != "upserted" || got.DeviceFlag != 7 || got.DeviceLevel != 8 {
		t.Fatalf("stored user = %#v", got)
	}
}

func TestApplyBatchTouchUserConversationActiveAtPreservesUpdatedAt(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	bsm, ok := mustNewStateMachine(t, db, 11).(multiraft.BatchStateMachine)
	if !ok {
		t.Fatal("state machine does not implement multiraft.BatchStateMachine")
	}

	if err := db.ForSlot(11).UpsertUserConversationState(ctx, metadb.UserConversationState{
		UID:          "u1",
		ChannelID:    "g1",
		ChannelType:  2,
		ReadSeq:      10,
		DeletedToSeq: 3,
		ActiveAt:     100,
		UpdatedAt:    200,
	}); err != nil {
		t.Fatalf("UpsertUserConversationState() error = %v", err)
	}

	cmds := []multiraft.Command{
		{
			SlotID: 11,
			Index:  1,
			Term:   1,
			Data: EncodeTouchUserConversationActiveAtCommand([]metadb.UserConversationActivePatch{
				{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 300},
			}),
		},
	}

	results, err := bsm.ApplyBatch(ctx, cmds)
	if err != nil {
		t.Fatalf("ApplyBatch() error = %v", err)
	}
	if len(results) != 1 || string(results[0]) != ApplyResultOK {
		t.Fatalf("ApplyBatch() results = %q", results)
	}

	got, err := db.ForSlot(11).GetUserConversationState(ctx, "u1", "g1", 2)
	if err != nil {
		t.Fatalf("GetUserConversationState() error = %v", err)
	}
	if got.ActiveAt != 300 {
		t.Fatalf("ActiveAt = %d, want 300", got.ActiveAt)
	}
	if got.UpdatedAt != 200 {
		t.Fatalf("UpdatedAt = %d, want 200", got.UpdatedAt)
	}
	if got.ReadSeq != 10 || got.DeletedToSeq != 3 {
		t.Fatalf("state = %#v", got)
	}
}

func TestApplyBatchUpsertChannelUpdateLogs(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	bsm, ok := mustNewStateMachine(t, db, 11).(multiraft.BatchStateMachine)
	if !ok {
		t.Fatal("state machine does not implement multiraft.BatchStateMachine")
	}

	cmds := []multiraft.Command{
		{
			SlotID: 11,
			Index:  1,
			Term:   1,
			Data: EncodeUpsertChannelUpdateLogsCommand([]metadb.ChannelUpdateLog{
				{ChannelID: "g1", ChannelType: 2, UpdatedAt: 100, LastMsgSeq: 10, LastClientMsgNo: "c1", LastMsgAt: 200},
				{ChannelID: "g1", ChannelType: 2, UpdatedAt: 110, LastMsgSeq: 11, LastClientMsgNo: "c2", LastMsgAt: 210},
				{ChannelID: "g2", ChannelType: 2, UpdatedAt: 120, LastMsgSeq: 20, LastClientMsgNo: "c3", LastMsgAt: 220},
			}),
		},
	}

	results, err := bsm.ApplyBatch(ctx, cmds)
	if err != nil {
		t.Fatalf("ApplyBatch() error = %v", err)
	}
	if len(results) != 1 || string(results[0]) != ApplyResultOK {
		t.Fatalf("ApplyBatch() results = %q", results)
	}

	got, err := db.ForSlot(11).BatchGetChannelUpdateLogs(ctx, []metadb.ConversationKey{
		{ChannelID: "g1", ChannelType: 2},
		{ChannelID: "g2", ChannelType: 2},
	})
	if err != nil {
		t.Fatalf("BatchGetChannelUpdateLogs() error = %v", err)
	}

	want := map[metadb.ConversationKey]metadb.ChannelUpdateLog{
		{ChannelID: "g1", ChannelType: 2}: {
			ChannelID:       "g1",
			ChannelType:     2,
			UpdatedAt:       110,
			LastMsgSeq:      11,
			LastClientMsgNo: "c2",
			LastMsgAt:       210,
		},
		{ChannelID: "g2", ChannelType: 2}: {
			ChannelID:       "g2",
			ChannelType:     2,
			UpdatedAt:       120,
			LastMsgSeq:      20,
			LastClientMsgNo: "c3",
			LastMsgAt:       220,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BatchGetChannelUpdateLogs() = %#v, want %#v", got, want)
	}
}

// --- Encode/decode edge case tests ---

func TestEncodeDecodeEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		user metadb.User
	}{
		{
			name: "empty UID and Token",
			user: metadb.User{UID: "", Token: "", DeviceFlag: 0, DeviceLevel: 0},
		},
		{
			name: "zero-value fields",
			user: metadb.User{UID: "u", Token: "t", DeviceFlag: 0, DeviceLevel: 0},
		},
		{
			name: "MaxInt64 fields",
			user: metadb.User{UID: "u", Token: "t", DeviceFlag: math.MaxInt64, DeviceLevel: math.MaxInt64},
		},
		{
			name: "negative int64 fields",
			user: metadb.User{UID: "u", Token: "t", DeviceFlag: math.MinInt64, DeviceLevel: -1},
		},
		{
			name: "long strings (1KB+)",
			user: metadb.User{UID: strings.Repeat("x", 1024), Token: strings.Repeat("y", 2048), DeviceFlag: 1, DeviceLevel: 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := EncodeUpsertUserCommand(tt.user)
			decoded, err := decodeCommand(encoded)
			if err != nil {
				t.Fatalf("decodeCommand() error = %v", err)
			}
			uc, ok := decoded.(*upsertUserCmd)
			if !ok {
				t.Fatalf("type = %T, want *upsertUserCmd", decoded)
			}
			if uc.user.UID != tt.user.UID {
				t.Fatalf("UID = %q, want %q", uc.user.UID, tt.user.UID)
			}
			if uc.user.Token != tt.user.Token {
				t.Fatalf("Token = %q, want %q", uc.user.Token, tt.user.Token)
			}
			if uc.user.DeviceFlag != tt.user.DeviceFlag {
				t.Fatalf("DeviceFlag = %d, want %d", uc.user.DeviceFlag, tt.user.DeviceFlag)
			}
			if uc.user.DeviceLevel != tt.user.DeviceLevel {
				t.Fatalf("DeviceLevel = %d, want %d", uc.user.DeviceLevel, tt.user.DeviceLevel)
			}
		})
	}
}

func TestApplyBatch_DeleteChannel(t *testing.T) {
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 1)
	ctx := context.Background()

	// First create a channel via upsert
	createCmd := EncodeUpsertChannelCommand(metadb.Channel{
		ChannelID: "ch1", ChannelType: 1, Ban: 0,
	})
	_, err := sm.Apply(ctx, multiraft.Command{SlotID: 1, Data: createCmd})
	if err != nil {
		t.Fatalf("Apply upsert: %v", err)
	}

	// Verify channel exists
	ch, err := db.ForSlot(1).GetChannel(ctx, "ch1", 1)
	if err != nil {
		t.Fatalf("GetChannel after create: %v", err)
	}
	if ch.ChannelID != "ch1" {
		t.Fatalf("unexpected channel ID: %s", ch.ChannelID)
	}

	addSubscribersCmd := EncodeAddSubscribersCommand("ch1", 1, []string{"u2", "u3"})
	_, err = sm.Apply(ctx, multiraft.Command{SlotID: 1, Data: addSubscribersCmd})
	if err != nil {
		t.Fatalf("Apply add subscribers: %v", err)
	}

	// Delete the channel
	deleteCmd := EncodeDeleteChannelCommand("ch1", 1)
	_, err = sm.Apply(ctx, multiraft.Command{SlotID: 1, Data: deleteCmd})
	if err != nil {
		t.Fatalf("Apply delete: %v", err)
	}

	// Verify channel is gone
	_, err = db.ForSlot(1).GetChannel(ctx, "ch1", 1)
	if err != metadb.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}

	uids, _, done, err := db.ForSlot(1).ListSubscribersPage(ctx, "ch1", 1, "", 10)
	if err != nil {
		t.Fatalf("ListSubscribersPage after delete: %v", err)
	}
	if len(uids) != 0 || !done {
		t.Fatalf("expected subscribers removed, got uids=%v done=%v", uids, done)
	}
}

func TestEncodeDecodeChannelEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		channel metadb.Channel
	}{
		{
			name:    "empty ChannelID",
			channel: metadb.Channel{ChannelID: "", ChannelType: 0, Ban: 0},
		},
		{
			name:    "MaxInt64 fields",
			channel: metadb.Channel{ChannelID: "c1", ChannelType: math.MaxInt64, Ban: math.MaxInt64},
		},
		{
			name:    "long ChannelID (1KB+)",
			channel: metadb.Channel{ChannelID: strings.Repeat("z", 1024), ChannelType: 1, Ban: 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := EncodeUpsertChannelCommand(tt.channel)
			decoded, err := decodeCommand(encoded)
			if err != nil {
				t.Fatalf("decodeCommand() error = %v", err)
			}
			cc, ok := decoded.(*upsertChannelCmd)
			if !ok {
				t.Fatalf("type = %T, want *upsertChannelCmd", decoded)
			}
			if cc.channel.ChannelID != tt.channel.ChannelID {
				t.Fatalf("ChannelID = %q, want %q", cc.channel.ChannelID, tt.channel.ChannelID)
			}
			if cc.channel.ChannelType != tt.channel.ChannelType {
				t.Fatalf("ChannelType = %d, want %d", cc.channel.ChannelType, tt.channel.ChannelType)
			}
			if cc.channel.Ban != tt.channel.Ban {
				t.Fatalf("Ban = %d, want %d", cc.channel.Ban, tt.channel.Ban)
			}
		})
	}
}
