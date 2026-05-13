package fsm

import (
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestDecodeCommandInspectionRedactsUserToken(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeUpsertUserCommand(metadb.User{
		UID:         "u1",
		Token:       "secret-token",
		DeviceFlag:  3,
		DeviceLevel: 7,
	}))
	require.NoError(t, err)

	require.Equal(t, CommandInspection{
		Type: "upsert_user",
		Payload: map[string]any{
			"command":      "upsert_user",
			"uid":          "u1",
			"token":        "***",
			"device_flag":  int64(3),
			"device_level": int64(7),
		},
	}, got)
}

func TestDecodeCommandInspectionIncludesChannelStatusFlags(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeUpsertChannelCommand(metadb.Channel{
		ChannelID: "room-status", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1,
	}))
	require.NoError(t, err)

	require.Equal(t, int64(1), got.Payload["ban"])
	require.Equal(t, int64(1), got.Payload["disband"])
	require.Equal(t, int64(1), got.Payload["send_ban"])
}

func TestDecodeCommandInspectionIncludesRuntimeMetaRetention(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeUpsertChannelRuntimeMetaCommand(metadb.ChannelRuntimeMeta{
		ChannelID:            "inspect-retention-meta",
		ChannelType:          2,
		ChannelEpoch:         3,
		LeaderEpoch:          4,
		Replicas:             []uint64{2, 1},
		ISR:                  []uint64{1, 2},
		Leader:               1,
		MinISR:               2,
		Status:               2,
		Features:             1,
		LeaseUntilMS:         1700000000000,
		RetentionThroughSeq:  99,
		RetentionUpdatedAtMS: 1700000000123,
		WriteFenceToken:      "task-inspect",
		WriteFenceVersion:    7,
		WriteFenceReason:     2,
		WriteFenceUntilMS:    1700000000456,
	}))
	require.NoError(t, err)

	require.Equal(t, uint64(99), got.Payload["retention_through_seq"])
	require.Equal(t, int64(1700000000123), got.Payload["retention_updated_at_ms"])
	require.Equal(t, "task-inspect", got.Payload["write_fence_token"])
	require.Equal(t, uint64(7), got.Payload["write_fence_version"])
	require.Equal(t, uint8(2), got.Payload["write_fence_reason"])
	require.Equal(t, int64(1700000000456), got.Payload["write_fence_until_ms"])
}

func TestDecodeCommandInspectionIncludesAdvanceChannelRetentionThroughSeq(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeAdvanceChannelRetentionThroughSeqCommand(metadb.ChannelRetentionAdvance{
		ChannelID:            "inspect-retention-advance",
		ChannelType:          2,
		ExpectedChannelEpoch: 3,
		ExpectedLeaderEpoch:  4,
		ExpectedLeader:       1,
		ExpectedLeaseUntilMS: 1700000000000,
		RetentionThroughSeq:  99,
		RetentionUpdatedAtMS: 1700000000123,
	}))
	require.NoError(t, err)

	require.Equal(t, CommandInspection{
		Type: "advance_channel_retention_through_seq",
		Payload: map[string]any{
			"command":                 "advance_channel_retention_through_seq",
			"channel_id":              "inspect-retention-advance",
			"channel_type":            int64(2),
			"expected_channel_epoch":  uint64(3),
			"expected_leader_epoch":   uint64(4),
			"expected_leader":         uint64(1),
			"expected_lease_until_ms": int64(1700000000000),
			"retention_through_seq":   uint64(99),
			"retention_updated_at_ms": int64(1700000000123),
		},
	}, got)
}

func TestDecodeCommandInspectionIncludesGuardedChannelMigrationCreate(t *testing.T) {
	task := metadb.ChannelMigrationTask{
		TaskID:      "task-inspect-create-guard",
		Kind:        metadb.ChannelMigrationKindReplicaReplace,
		Status:      metadb.ChannelMigrationStatusPending,
		Phase:       metadb.ChannelMigrationPhaseValidate,
		ChannelID:   "inspect-create-guard",
		ChannelType: 2,
		SourceNode:  1,
		TargetNode:  3,
		CreatedAtMS: 1700000000000,
		UpdatedAtMS: 1700000000000,
	}
	got, err := DecodeCommandInspection(EncodeCreateChannelMigrationTaskWithRuntimeGuardCommand(metadb.ChannelMigrationTaskCreate{
		Task: task,
		RuntimeGuard: metadb.ChannelMigrationRuntimeGuard{
			ChannelID:            task.ChannelID,
			ChannelType:          task.ChannelType,
			ExpectedChannelEpoch: 7,
			ExpectedLeaderEpoch:  9,
			ExpectedLeader:       1,
		},
	}))
	require.NoError(t, err)

	require.Equal(t, "create_channel_migration_task_with_runtime_guard", got.Type)
	require.Equal(t, task.TaskID, got.Payload["task_id"])
	require.Equal(t, task.ChannelID, got.Payload["channel_id"])
}

func TestDecodeCommandInspectionIncludesUserConversationActiveMessageSeq(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeTouchUserConversationActiveAtCommand([]metadb.UserConversationActivePatch{{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    100,
		MessageSeq:  11,
	}}))
	require.NoError(t, err)

	require.Equal(t, CommandInspection{
		Type: "touch_user_conversation_active_at",
		Payload: map[string]any{
			"command": "touch_user_conversation_active_at",
			"patches": []map[string]any{{
				"uid":          "u1",
				"channel_id":   "g1",
				"channel_type": int64(2),
				"active_at":    int64(100),
				"message_seq":  uint64(11),
			}},
		},
	}, got)
}

func TestDecodeCommandInspectionMarksReservedConversationProjectionCommand(t *testing.T) {
	got, err := DecodeCommandInspection([]byte{commandVersion, cmdTypeReservedConversationProjectionUpsert})
	require.NoError(t, err)

	require.Equal(t, CommandInspection{
		Type: "reserved_conversation_projection",
		Payload: map[string]any{
			"command":    "reserved_conversation_projection",
			"deprecated": true,
			"reserved":   true,
		},
	}, got)
}

func TestDecodeCommandInspectionIncludesHideUserConversation(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeHideUserConversationsCommand([]metadb.UserConversationDelete{{
		UID:          "u1",
		ChannelID:    "g1",
		ChannelType:  2,
		DeletedToSeq: 10,
		UpdatedAt:    20,
	}}))
	require.NoError(t, err)

	require.Equal(t, CommandInspection{
		Type: "hide_user_conversations",
		Payload: map[string]any{
			"command": "hide_user_conversations",
			"deletes": []map[string]any{{
				"uid":            "u1",
				"channel_id":     "g1",
				"channel_type":   int64(2),
				"deleted_to_seq": uint64(10),
				"updated_at":     int64(20),
			}},
		},
	}, got)
}

func TestDecodeCommandInspectionExpandsApplyDeltaOriginalCommand(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeApplyDeltaCommand(
		11,
		99,
		7,
		EncodeUpsertChannelCommand(metadb.Channel{ChannelID: "room-1", ChannelType: 2, Ban: 1}),
	))
	require.NoError(t, err)

	require.Equal(t, CommandInspection{
		Type: "apply_delta",
		Payload: map[string]any{
			"command":        "apply_delta",
			"source_slot_id": multiraft.SlotID(11),
			"source_index":   uint64(99),
			"hash_slot":      uint16(7),
			"original": map[string]any{
				"command":      "upsert_channel",
				"channel_id":   "room-1",
				"channel_type": int64(2),
				"ban":          int64(1),
				"disband":      int64(0),
				"send_ban":     int64(0),
			},
		},
	}, got)
}
