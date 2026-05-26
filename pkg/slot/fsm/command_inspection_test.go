package fsm

import (
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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
		ChannelID: "room-status", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, AllowStranger: 1,
	}))
	require.NoError(t, err)

	require.Equal(t, int64(1), got.Payload["ban"])
	require.Equal(t, int64(1), got.Payload["disband"])
	require.Equal(t, int64(1), got.Payload["send_ban"])
	require.Equal(t, int64(1), got.Payload["allow_stranger"])
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
		RouteGeneration:      42,
	}))
	require.NoError(t, err)

	require.Equal(t, uint64(99), got.Payload["retention_through_seq"])
	require.Equal(t, int64(1700000000123), got.Payload["retention_updated_at_ms"])
	require.Equal(t, "task-inspect", got.Payload["write_fence_token"])
	require.Equal(t, uint64(7), got.Payload["write_fence_version"])
	require.Equal(t, uint8(2), got.Payload["write_fence_reason"])
	require.Equal(t, int64(1700000000456), got.Payload["write_fence_until_ms"])
	require.Equal(t, uint64(42), got.Payload["route_generation"])
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

func TestDecodeCommandInspectionIncludesCMDConversationStateCommands(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeUpsertCMDConversationStatesCommand([]metadb.CMDConversationState{{
		UID:          "u1",
		ChannelID:    "g1____cmd",
		ChannelType:  2,
		ReadSeq:      3,
		DeletedToSeq: 4,
		ActiveAt:     5,
		UpdatedAt:    6,
	}}))
	require.NoError(t, err)
	require.Equal(t, CommandInspection{
		Type: "upsert_cmd_conversation_states",
		Payload: map[string]any{
			"command": "upsert_cmd_conversation_states",
			"states": []map[string]any{{
				"uid":            "u1",
				"channel_id":     "g1____cmd",
				"channel_type":   int64(2),
				"read_seq":       uint64(3),
				"deleted_to_seq": uint64(4),
				"active_at":      int64(5),
				"updated_at":     int64(6),
			}},
		},
	}, got)

	got, err = DecodeCommandInspection(EncodeAdvanceCMDConversationReadSeqCommand([]metadb.CMDConversationReadPatch{{
		UID:         "u1",
		ChannelID:   "g1____cmd",
		ChannelType: 2,
		ReadSeq:     9,
		UpdatedAt:   10,
	}}))
	require.NoError(t, err)
	require.Equal(t, CommandInspection{
		Type: "advance_cmd_conversation_read_seq",
		Payload: map[string]any{
			"command": "advance_cmd_conversation_read_seq",
			"patches": []map[string]any{{
				"uid":          "u1",
				"channel_id":   "g1____cmd",
				"channel_type": int64(2),
				"read_seq":     uint64(9),
				"updated_at":   int64(10),
			}},
		},
	}, got)
}

func TestDecodeCommandInspectionIncludesPluginBindingCommands(t *testing.T) {
	binding := metadb.PluginUserBinding{
		UID:         "u1",
		PluginNo:    "bot-a",
		CreatedAtMS: 100,
		UpdatedAtMS: 101,
	}
	got, err := DecodeCommandInspection(EncodeBindPluginUserCommand(binding))
	require.NoError(t, err)
	require.Equal(t, CommandInspection{
		Type: "bind_plugin_user",
		Payload: map[string]any{
			"command":       "bind_plugin_user",
			"uid":           "u1",
			"plugin_no":     "bot-a",
			"created_at_ms": int64(100),
			"updated_at_ms": int64(101),
		},
	}, got)

	got, err = DecodeCommandInspection(EncodeUnbindPluginUserCommand("u1", "bot-a"))
	require.NoError(t, err)
	require.Equal(t, CommandInspection{
		Type: "unbind_plugin_user",
		Payload: map[string]any{
			"command":   "unbind_plugin_user",
			"uid":       "u1",
			"plugin_no": "bot-a",
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
				"command":        "upsert_channel",
				"channel_id":     "room-1",
				"channel_type":   int64(2),
				"ban":            int64(1),
				"disband":        int64(0),
				"send_ban":       int64(0),
				"allow_stranger": int64(0),
			},
		},
	}, got)
}

func TestDecodeCommandInspectionExpandsApplyDeltaPluginBindingCommand(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeApplyDeltaCommand(
		11,
		99,
		7,
		EncodeBindPluginUserCommand(metadb.PluginUserBinding{
			UID:         "u1",
			PluginNo:    "bot-a",
			CreatedAtMS: 100,
			UpdatedAtMS: 101,
		}),
	))
	require.NoError(t, err)

	require.Equal(t, "apply_delta", got.Type)
	require.Equal(t, map[string]any{
		"command":       "bind_plugin_user",
		"uid":           "u1",
		"plugin_no":     "bot-a",
		"created_at_ms": int64(100),
		"updated_at_ms": int64(101),
	}, got.Payload["original"])
}
