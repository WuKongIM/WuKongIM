package fsm

import (
	"encoding/json"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/stretchr/testify/require"
)

// TestCommandInspectionCoversRegisteredDecoders requires every wire command to
// have an encoded fixture and a usable, redacted operator view.
func TestCommandInspectionCoversRegisteredDecoders(t *testing.T) {
	checked := func(data []byte, err error) []byte {
		t.Helper()
		require.NoError(t, err)
		return data
	}
	user := metadb.User{UID: "u1", Token: "inspection-secret"}
	device := metadb.Device{UID: "u1", Token: "inspection-secret"}
	channel := metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1}
	meta := fsmTestRuntimeMeta("g1", 2)
	members := []metadb.UserChannelMembership{{UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 4, ReadSeq: 8, DeletedToSeq: 9, ActivatedAt: 150, UpdatedAt: 170}}
	cmdMembers := []metadb.UserCMDChannelMembership{{UID: "u1", CommandChannelID: "g1____cmd", ChannelType: 2, StartSeq: 4, AckSeq: 8, UpdatedAt: 170}}
	latest := metadb.ChannelLatest{ChannelID: "g1", ChannelType: 2, LastMessageSeq: 10, Payload: []byte("inspection-secret")}
	event := metadb.MessageEventAppend{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1", EventID: "evt-1", EventKey: "main", EventType: metadb.EventTypeStreamDelta, Visibility: metadb.VisibilityPublic, OccurredAt: 10, Payload: []byte("inspection-secret"), UpdatedAt: 11}
	task := fsmTestChannelMigrationTask("task-1", "g1")
	fenced := fsmTestFencedRuntimeMeta("g1", 2, task.TaskID, 7)
	personID := channelid.EncodePersonChannel("u1", "u2")
	personMeta := fsmTestRuntimeMeta(personID, 1)
	fixtures := []struct {
		name string
		data []byte
	}{
		{"message_update", checked(EncodeMessageUpdateCommand(metadb.MessageUpdateMutation{Op: "init", ChannelID: "g1", ChannelType: 2, Generation: "generation"}))},
		{"noop", EncodeNoopCommand()},
		{"upsert_user", EncodeUpsertUserCommand(user)},
		{"create_user", EncodeCreateUserCommand(user)},
		{"upsert_device", EncodeUpsertDeviceCommand(device)},
		{"upsert_channel", EncodeUpsertChannelCommand(channel)},
		{"create_channel", EncodeCreateChannelCommand(channel)},
		{"patch_channel_business_flags", EncodePatchChannelBusinessFlagsCommand("g1", 2, metadb.ChannelBusinessFlags{Ban: 1, SendBan: 1})},
		{"delete_channel", EncodeDeleteChannelCommand("g1", 2)},
		{"upsert_channel_runtime_meta", EncodeUpsertChannelRuntimeMetaCommand(meta)},
		{"create_channel_runtime_meta_batch", checked(EncodeCreateChannelRuntimeMetaBatchCommandChecked([]CreateChannelRuntimeMetaBatchItem{{HashSlot: 7, Meta: meta}}))},
		{"delete_channel_runtime_meta", EncodeDeleteChannelRuntimeMetaCommand("g1", 2)},
		{"advance_channel_retention_through_seq", EncodeAdvanceChannelRetentionThroughSeqCommand(metadb.ChannelRetentionAdvance{ChannelID: "g1", ChannelType: 2, RetentionThroughSeq: 9})},
		{"add_subscribers", EncodeAddSubscribersCommand("g1", 2, []string{"u1"})},
		{"remove_subscribers", EncodeRemoveSubscribersCommand("g1", 2, []string{"u1"})},
		{"upsert_user_channel_memberships", EncodeUpsertUserChannelMembershipsCommand(members)},
		{"delete_user_channel_memberships", EncodeDeleteUserChannelMembershipsCommand(members)},
		{"advance_user_channel_membership_read_seq", EncodeAdvanceUserChannelMembershipReadSeqCommand(members)},
		{"hide_user_channel_membership", EncodeHideUserChannelMembershipCommand(members)},
		{"activate_user_channel_membership", EncodeActivateUserChannelMembershipCommand(members)},
		{"upsert_user_cmd_channel_memberships", EncodeUpsertUserCMDChannelMembershipsCommand(cmdMembers)},
		{"advance_user_cmd_channel_membership_acks", EncodeAdvanceUserCMDChannelMembershipAcksCommand(cmdMembers)},
		{"tombstone_user_cmd_channel_memberships", EncodeTombstoneUserCMDChannelMembershipsCommand(cmdMembers)},
		{"upsert_channel_latest", EncodeUpsertChannelLatestCommand(latest)},
		{"upsert_channel_latest_batch", EncodeUpsertChannelLatestBatchCommand([]ChannelLatestBatchItem{{HashSlot: 7, Latest: latest}})},
		{"append_message_event", EncodeAppendMessageEventCommand(event)},
		{"append_message_events_batch", EncodeAppendMessageEventsCommand([]metadb.MessageEventAppend{event})},
		{"admit_person_directory_task_batch", checked(EncodeAdmitPersonDirectoryTaskBatchCommandChecked([]PersonDirectoryAdmissionBatchItem{{HashSlot: 7, Task: metadb.PersonDirectoryTask{ChannelID: personID, ChannelType: 1, CommittedTail: 9}, RuntimeMeta: personMeta}}))},
		{"ensure_user_channel_membership_batch", checked(EncodeEnsureUserChannelMembershipBatchCommandChecked([]UserChannelMembershipBatchItem{{HashSlot: 7, Membership: metadb.UserChannelMembership{UID: "u1", ChannelID: personID, ChannelType: 1, JoinSeq: 10}}}))},
		{"complete_person_directory_task_batch", checked(EncodeCompletePersonDirectoryTaskBatchCommandChecked([]PersonDirectoryCompletionBatchItem{{HashSlot: 7, ChannelID: personID, ChannelType: 1, Generation: 1}}))},
		{"bind_plugin_user", EncodeBindPluginUserCommand(metadb.PluginUserBinding{UID: "u1", PluginNo: "bot-1"})},
		{"unbind_plugin_user", EncodeUnbindPluginUserCommand("u1", "bot-1")},
		{"apply_delta", EncodeApplyDeltaCommand(1, 2, 7, EncodeUpsertChannelLatestCommand(latest))},
		{"enter_fence", EncodeEnterFenceCommandForTarget(7, 2)},
		{"ack_migration_outbox", EncodeAckHashSlotMigrationOutboxCommand(7, 1, 2, 3)},
		{"cleanup_migration_outbox", EncodeCleanupHashSlotMigrationOutboxCommand(7, 1, 2, 3)},
		{"create_channel_migration_task", EncodeCreateChannelMigrationTaskCommand(task)},
		{"create_channel_migration_task_with_runtime_guard", EncodeCreateChannelMigrationTaskWithRuntimeGuardCommand(metadb.ChannelMigrationTaskCreate{Task: task})},
		{"claim_channel_migration_task", EncodeClaimChannelMigrationTaskCommand(fsmTestChannelMigrationClaim(task, 2, 1750000006000, 1750000002000))},
		{"advance_channel_migration_task", EncodeAdvanceChannelMigrationTaskCommand(fsmTestChannelMigrationAdvance(task, metadb.ChannelMigrationStatusRunning, metadb.ChannelMigrationPhaseWarmCatchUp, 1750000002000))},
		{"set_channel_write_fence", EncodeSetChannelWriteFenceCommand(fsmTestSetFenceRequest(task, meta, 1750000007000, 1750000002000))},
		{"reset_channel_write_fence", EncodeResetChannelWriteFenceToPreCutoverCommand(fsmTestResetFenceRequest(task, fenced, metadb.ChannelMigrationPhaseWarmCatchUp, 1750000003000))},
		{"commit_channel_leader_transfer", EncodeCommitChannelLeaderTransferCommand(fsmTestCommitLeaderRequest(task, fenced, 1750000003000))},
		{"add_channel_learner", EncodeAddChannelLearnerCommand(fsmTestAddLearnerRequest(task, meta, 1750000003000))},
		{"promote_learner_and_remove_replica", EncodePromoteLearnerAndRemoveReplicaCommand(fsmTestPromoteRequest(task, fenced, 1750000003000))},
		{"clear_channel_write_fence", EncodeClearChannelWriteFenceCommand(fsmTestClearFenceRequest(task, fenced, 1750000003000))},
		{"abort_channel_migration", EncodeAbortChannelMigrationCommand(fsmTestAbortRequest(task, meta, 1750000003000))},
		{"garbage_collect_terminal_channel_migration_tasks", EncodeGarbageCollectTerminalChannelMigrationTasksCommand(metadb.ChannelMigrationTaskGCRequest{BeforeMS: 1750000010000, Limit: 10})},
	}
	seen := make(map[uint8]bool)
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			require.GreaterOrEqual(t, len(fixture.data), headerSize)
			kind := fixture.data[1]
			require.False(t, seen[kind], "duplicate command fixture %d", kind)
			seen[kind] = true
			require.Contains(t, commandDecoders, kind)
			_, err := decodeCommand(fixture.data)
			require.NoError(t, err, "fixture must reach the inspection path")
			got, err := DecodeCommandInspection(fixture.data)
			require.NoError(t, err)
			require.Equal(t, fixture.name, got.Type)
			require.Equal(t, fixture.name, got.Payload["command"])
			encoded, err := json.Marshal(got.Payload)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "inspection-secret")
		})
	}
	for kind := range commandDecoders {
		require.True(t, seen[kind], "registered command %d needs an inspection fixture", kind)
	}
}

func TestCommandInspectionMembershipProgress(t *testing.T) {
	members := []metadb.UserChannelMembership{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ReadSeq: 42, DeletedToSeq: 30, ActivatedAt: 150, UpdatedAt: 170},
		{UID: "u1", ChannelID: "g2", ChannelType: 2, ReadSeq: 99, UpdatedAt: 180},
	}
	for _, encode := range []func([]metadb.UserChannelMembership) []byte{EncodeUpsertUserChannelMembershipsCommand, EncodeDeleteUserChannelMembershipsCommand, EncodeAdvanceUserChannelMembershipReadSeqCommand, EncodeHideUserChannelMembershipCommand, EncodeActivateUserChannelMembershipCommand} {
		got, err := DecodeCommandInspection(encode(members))
		require.NoError(t, err)
		items := got.Payload["memberships"].([]map[string]any)
		require.Len(t, items, 2)
		for i, member := range members {
			require.Equal(t, member.UID, items[i]["uid"])
			require.Equal(t, member.ChannelID, items[i]["channel_id"])
			require.Equal(t, member.ReadSeq, items[i]["read_seq"])
			require.Equal(t, member.DeletedToSeq, items[i]["deleted_to_seq"])
			require.Equal(t, member.ActivatedAt, items[i]["activated_at"])
			require.Equal(t, member.UpdatedAt, items[i]["updated_at"])
		}
	}
}

func TestCommandInspectionLatestRedactsMessageBody(t *testing.T) {
	latest := metadb.ChannelLatest{ChannelID: "g1", ChannelType: 2, LastMessageID: 123, LastMessageSeq: 42, LastAt: 150, FromUID: "u1", ClientMsgNo: "cmn-1", Payload: []byte("private-message"), UpdatedAt: 170}
	for _, data := range [][]byte{EncodeUpsertChannelLatestCommand(latest), EncodeUpsertChannelLatestBatchCommand([]ChannelLatestBatchItem{{HashSlot: 7, Latest: latest}})} {
		got, err := DecodeCommandInspection(data)
		require.NoError(t, err)
		payload := got.Payload
		if got.Type == "upsert_channel_latest_batch" {
			items := payload["items"].([]map[string]any)
			require.Len(t, items, 1)
			payload = items[0]
			require.Equal(t, uint16(7), payload["hash_slot"])
		}
		require.Equal(t, latest.LastMessageID, payload["last_message_id"])
		require.Equal(t, latest.LastMessageSeq, payload["last_message_seq"])
		require.Equal(t, latest.FromUID, payload["from_uid"])
		require.Equal(t, len(latest.Payload), payload["payload_bytes"])
		require.NotContains(t, payload, "payload")
		encoded, err := json.Marshal(got.Payload)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "private-message")
	}
}

func TestCommandInspectionCMDMembershipProgress(t *testing.T) {
	members := []metadb.UserCMDChannelMembership{{UID: "u1", CommandChannelID: "g1____cmd", ChannelType: 2, StartSeq: 4, AckSeq: 42, Tombstone: true, TombstoneAt: 160, UpdatedAt: 170}}
	for _, encode := range []func([]metadb.UserCMDChannelMembership) []byte{EncodeUpsertUserCMDChannelMembershipsCommand, EncodeAdvanceUserCMDChannelMembershipAcksCommand, EncodeTombstoneUserCMDChannelMembershipsCommand} {
		got, err := DecodeCommandInspection(encode(members))
		require.NoError(t, err)
		require.Equal(t, []map[string]any{{
			"uid": "u1", "command_channel_id": "g1____cmd", "channel_type": int64(2),
			"start_seq": uint64(4), "ack_seq": uint64(42), "tombstone": true,
			"tombstone_at": int64(160), "updated_at": int64(170),
		}}, got.Payload["memberships"])
	}
}

func TestCommandInspectionChannelFlagsAndMigrationCleanup(t *testing.T) {
	for _, tc := range []struct {
		data []byte
		want map[string]any
	}{
		{EncodeCreateChannelCommand(metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1, SendBan: 1, Large: 1}), map[string]any{"command": "create_channel", "channel_id": "g1", "channel_type": int64(2), "ban": int64(1), "disband": int64(0), "send_ban": int64(1), "allow_stranger": int64(0), "large": int64(1)}},
		{EncodePatchChannelBusinessFlagsCommand("g1", 2, metadb.ChannelBusinessFlags{Ban: 1, SendBan: 1}), map[string]any{"command": "patch_channel_business_flags", "channel_id": "g1", "channel_type": int64(2), "ban": int64(1), "disband": int64(0), "send_ban": int64(1)}},
		{EncodeGarbageCollectTerminalChannelMigrationTasksCommand(metadb.ChannelMigrationTaskGCRequest{BeforeMS: 1750000010000, Limit: 10}), map[string]any{"command": "garbage_collect_terminal_channel_migration_tasks", "before_ms": int64(1750000010000), "limit": 10}},
	} {
		got, err := DecodeCommandInspection(tc.data)
		require.NoError(t, err)
		require.Equal(t, tc.want, got.Payload)
	}
}

func TestCommandInspectionUnsupportedFormatsStillFailFSMDecoding(t *testing.T) {
	for _, tc := range []struct {
		data        []byte
		decodeError error
	}{
		{[]byte{commandVersion + 1, cmdTypeNoop}, metadb.ErrCorruptValue},
		{[]byte{commandVersion, 255}, metadb.ErrInvalidArgument},
	} {
		_, err := DecodeCommandInspection(tc.data)
		require.ErrorIs(t, err, ErrCommandInspectionUnsupported)
		_, err = decodeCommand(tc.data)
		require.ErrorIs(t, err, tc.decodeError)
	}
	_, err := DecodeCommandInspection([]byte{commandVersion})
	require.ErrorIs(t, err, metadb.ErrCorruptValue)
	require.NotErrorIs(t, err, ErrCommandInspectionUnsupported)
}
