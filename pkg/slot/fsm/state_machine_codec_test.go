package fsm

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/hashslot"
	"github.com/stretchr/testify/require"
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
		ChannelID: "c1", ChannelType: 1, ChannelEpoch: 3, LeaderEpoch: 2,
		Replicas: []uint64{3, 1, 2}, ISR: []uint64{2, 1}, Leader: 1, MinISR: 2,
		Status: 3, Features: 9, LeaseUntilMS: 1700000000000,
	})
	decoded, err = decodeCommand(metaCmd)
	if err != nil {
		t.Fatalf("decodeCommand(runtime_meta) error = %v", err)
	}
	mc, ok := decoded.(*upsertChannelRuntimeMetaCmd)
	if !ok {
		t.Fatalf("decodeCommand(runtime_meta) type = %T, want *upsertChannelRuntimeMetaCmd", decoded)
	}
	wantMeta := metadb.NormalizeChannelRuntimeMeta(metadb.ChannelRuntimeMeta{
		ChannelID: "c1", ChannelType: 1, ChannelEpoch: 3, LeaderEpoch: 2,
		Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2}, Leader: 1, MinISR: 2,
		Status: 3, Features: 9, LeaseUntilMS: 1700000000000,
	})
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
	if !ok || applyDelta.SourceSlotID != 11 || applyDelta.SourceIndex != 7 || applyDelta.HashSlot != 5 {
		t.Fatalf("decoded apply delta = %#v", decoded)
	}

	enterFenceData := EncodeEnterFenceCommand(5)
	decoded, err = decodeCommand(enterFenceData)
	if err != nil {
		t.Fatalf("decodeCommand(enter_fence) error = %v", err)
	}
	enterFence, ok := decoded.(*enterFenceCmd)
	if !ok || enterFence.HashSlot != 5 {
		t.Fatalf("decoded enter fence = %#v", decoded)
	}
}

func TestStateMachineEncodeNoopCommand(t *testing.T) {
	decoded, err := decodeCommand(EncodeNoopCommand())
	if err != nil {
		t.Fatalf("decodeCommand(noop) error = %v", err)
	}
	if _, ok := decoded.(*noopCmd); !ok {
		t.Fatalf("decodeCommand(noop) type = %T, want *noopCmd", decoded)
	}
}

func TestDecodeUpsertChannelRuntimeMetaCommandPreservesRetentionFields(t *testing.T) {
	meta := metadb.ChannelRuntimeMeta{
		ChannelID: "decode-retention", ChannelType: 2, ChannelEpoch: 3, LeaderEpoch: 4,
		Replicas: []uint64{2, 1}, ISR: []uint64{1, 2}, Leader: 1, MinISR: 2,
		Status: 2, Features: 1, LeaseUntilMS: 1700000000000,
		RetentionThroughSeq: 99, RetentionUpdatedAtMS: 1700000000123,
	}
	decoded, err := decodeCommand(EncodeUpsertChannelRuntimeMetaCommand(meta))
	if err != nil {
		t.Fatalf("decodeCommand(runtime_meta retention) error = %v", err)
	}
	cmd, ok := decoded.(*upsertChannelRuntimeMetaCmd)
	if !ok {
		t.Fatalf("decodeCommand(runtime_meta retention) type = %T, want *upsertChannelRuntimeMetaCmd", decoded)
	}
	if cmd.meta.RetentionThroughSeq != meta.RetentionThroughSeq || cmd.meta.RetentionUpdatedAtMS != meta.RetentionUpdatedAtMS {
		t.Fatalf("decoded retention = (%d,%d), want (%d,%d)", cmd.meta.RetentionThroughSeq, cmd.meta.RetentionUpdatedAtMS, meta.RetentionThroughSeq, meta.RetentionUpdatedAtMS)
	}
}

func TestDecodeUpsertChannelRuntimeMetaCommandPreservesRouteGeneration(t *testing.T) {
	meta := metadb.ChannelRuntimeMeta{
		ChannelID: "decode-route-generation", ChannelType: 2, ChannelEpoch: 3, LeaderEpoch: 4,
		Replicas: []uint64{2, 1}, ISR: []uint64{1, 2}, Leader: 1, MinISR: 2,
		Status: 2, Features: 1, LeaseUntilMS: 1700000000000, RouteGeneration: 42,
	}
	decoded, err := decodeCommand(EncodeUpsertChannelRuntimeMetaCommand(meta))
	require.NoError(t, err)
	cmd, ok := decoded.(*upsertChannelRuntimeMetaCmd)
	require.True(t, ok)
	require.Equal(t, uint64(42), cmd.meta.RouteGeneration)
}

func TestDecodeUpsertChannelRuntimeMetaCommandDefaultsMissingRetentionFieldsToZero(t *testing.T) {
	data := make([]byte, 0, headerSize+128)
	data = append(data, commandVersion, cmdTypeUpsertChannelRuntimeMeta)
	data = appendStringTLVField(data, tagRuntimeMetaChannelID, "old-runtime-meta")
	data = appendInt64TLVField(data, tagRuntimeMetaChannelType, 2)
	data = appendUint64TLVField(data, tagRuntimeMetaChannelEpoch, 3)
	data = appendUint64TLVField(data, tagRuntimeMetaLeaderEpoch, 4)
	data = appendBytesTLVField(data, tagRuntimeMetaReplicas, encodeUint64Slice([]uint64{1, 2}))
	data = appendBytesTLVField(data, tagRuntimeMetaISR, encodeUint64Slice([]uint64{1, 2}))
	data = appendUint64TLVField(data, tagRuntimeMetaLeader, 1)
	data = appendInt64TLVField(data, tagRuntimeMetaMinISR, 2)
	data = appendUint64TLVField(data, tagRuntimeMetaStatus, 2)
	data = appendUint64TLVField(data, tagRuntimeMetaFeatures, 1)
	data = appendInt64TLVField(data, tagRuntimeMetaLeaseUntilMS, 1700000000000)

	decoded, err := decodeCommand(data)
	if err != nil {
		t.Fatalf("decodeCommand(old runtime_meta) error = %v", err)
	}
	cmd, ok := decoded.(*upsertChannelRuntimeMetaCmd)
	if !ok || cmd.meta.RetentionThroughSeq != 0 || cmd.meta.RetentionUpdatedAtMS != 0 {
		t.Fatalf("decoded old runtime meta = %#v, want zero retention fields", decoded)
	}
}

func TestDecodeAdvanceChannelRetentionThroughSeqCommand(t *testing.T) {
	req := metadb.ChannelRetentionAdvance{
		ChannelID: "decode-retention-advance", ChannelType: 2,
		ExpectedChannelEpoch: 3, ExpectedLeaderEpoch: 4, ExpectedLeader: 1,
		ExpectedLeaseUntilMS: 1700000000000, RetentionThroughSeq: 99, RetentionUpdatedAtMS: 1700000000123,
	}
	decoded, err := decodeCommand(EncodeAdvanceChannelRetentionThroughSeqCommand(req))
	if err != nil {
		t.Fatalf("decodeCommand(advance retention) error = %v", err)
	}
	cmd, ok := decoded.(*advanceChannelRetentionThroughSeqCmd)
	if !ok || !reflect.DeepEqual(cmd.req, req) {
		t.Fatalf("decoded advance retention = %#v, want %#v", decoded, req)
	}
}

func TestEncodeDecodeEdgeCases(t *testing.T) {
	tests := []metadb.User{
		{UID: "", Token: "", DeviceFlag: 0, DeviceLevel: 0},
		{UID: "u", Token: "t", DeviceFlag: 0, DeviceLevel: 0},
		{UID: "u", Token: "t", DeviceFlag: math.MaxInt64, DeviceLevel: math.MaxInt64},
		{UID: "u", Token: "t", DeviceFlag: math.MinInt64, DeviceLevel: -1},
		{UID: strings.Repeat("x", 1024), Token: strings.Repeat("y", 2048), DeviceFlag: 1, DeviceLevel: 2},
	}
	for _, user := range tests {
		decoded, err := decodeCommand(EncodeUpsertUserCommand(user))
		if err != nil {
			t.Fatalf("decodeCommand() error = %v", err)
		}
		cmd, ok := decoded.(*upsertUserCmd)
		if !ok || cmd.user != user {
			t.Fatalf("decoded user = %#v, want %#v", decoded, user)
		}
	}
}

func TestEncodeDecodeChannelStatusFlags(t *testing.T) {
	want := metadb.Channel{ChannelID: "c-status", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, AllowStranger: 1, Large: 1}
	decoded, err := decodeCommand(EncodeUpsertChannelCommand(want))
	if err != nil {
		t.Fatalf("decodeCommand(): %v", err)
	}
	cmd, ok := decoded.(*upsertChannelCmd)
	if !ok || cmd.channel != want {
		t.Fatalf("decoded channel = %#v, want %#v", decoded, want)
	}
}

func TestEncodeDecodeChannelEdgeCases(t *testing.T) {
	tests := []metadb.Channel{
		{ChannelID: "", ChannelType: 0, Ban: 0},
		{ChannelID: "c1", ChannelType: math.MaxInt64, Ban: math.MaxInt64},
		{ChannelID: strings.Repeat("z", 1024), ChannelType: 1, Ban: 0},
	}
	for _, channel := range tests {
		decoded, err := decodeCommand(EncodeUpsertChannelCommand(channel))
		if err != nil {
			t.Fatalf("decodeCommand() error = %v", err)
		}
		cmd, ok := decoded.(*upsertChannelCmd)
		if !ok || cmd.channel.ChannelID != channel.ChannelID || cmd.channel.ChannelType != channel.ChannelType || cmd.channel.Ban != channel.Ban {
			t.Fatalf("decoded channel = %#v, want %#v", decoded, channel)
		}
	}
}

func TestConversationBatchCheckedEncoderRejectsOwnedHashSlotMismatch(t *testing.T) {
	const hashSlotCount uint16 = 256
	uidForFive := uidForHashSlot(t, hashSlotCount, 5)
	uidForSeven := uidForHashSlot(t, hashSlotCount, 7)

	_, err := EncodeUpsertConversationStateBatchCommandChecked(hashSlotCount, []ConversationStateBatchItem{
		{HashSlot: 7, State: metadb.ConversationState{UID: uidForFive, Kind: metadb.ConversationKindNormal, ChannelID: "g", ChannelType: 2}},
	})
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("EncodeUpsertConversationStateBatchCommandChecked() error = %v, want ErrInvalidArgument", err)
	}

	_, err = EncodeTouchConversationActiveAtBatchCommandChecked(hashSlotCount, []ConversationActivePatchBatchItem{
		{HashSlot: 5, Patch: metadb.ConversationActivePatch{UID: uidForSeven, Kind: metadb.ConversationKindNormal, ChannelID: "g", ChannelType: 2}},
	})
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("EncodeTouchConversationActiveAtBatchCommandChecked() error = %v, want ErrInvalidArgument", err)
	}

	_, err = EncodeHideConversationBatchCommandChecked(hashSlotCount, []ConversationDeleteBatchItem{
		{HashSlot: 5, Delete: metadb.ConversationDelete{UID: uidForSeven, Kind: metadb.ConversationKindNormal, ChannelID: "g", ChannelType: 2}},
	})
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("EncodeHideConversationBatchCommandChecked() error = %v, want ErrInvalidArgument", err)
	}
}

func uidForHashSlot(t testing.TB, count, want uint16) string {
	t.Helper()
	for i := 0; i < 10000; i++ {
		uid := fmt.Sprintf("conversation-hash-%d-%d", want, i)
		if hashslot.HashSlotForKey(uid, count) == want {
			return uid
		}
	}
	t.Fatalf("could not find uid for hash slot %d/%d", want, count)
	return ""
}
