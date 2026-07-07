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
		ChannelID: "room-status", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, AllowStranger: 1, Large: 1,
	}))
	require.NoError(t, err)

	require.Equal(t, int64(1), got.Payload["ban"])
	require.Equal(t, int64(1), got.Payload["disband"])
	require.Equal(t, int64(1), got.Payload["send_ban"])
	require.Equal(t, int64(1), got.Payload["allow_stranger"])
	require.Equal(t, int64(1), got.Payload["large"])
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

func TestDecodeCommandInspectionIncludesConversationActiveKindAndMessageSeq(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeTouchConversationActiveAtCommand([]metadb.ConversationActivePatch{{
		UID:             "u1",
		Kind:            metadb.ConversationKindNormal,
		ChannelID:       "g1",
		ChannelType:     2,
		ReadSeq:         7,
		DeletedToSeq:    8,
		ActiveAt:        100,
		UpdatedAt:       101,
		MessageSeq:      11,
		SparseActive:    true,
		SparseActiveSet: true,
	}}))
	require.NoError(t, err)

	require.Equal(t, CommandInspection{
		Type: "touch_conversation_active_at",
		Payload: map[string]any{
			"command": "touch_conversation_active_at",
			"patches": []map[string]any{{
				"uid":               "u1",
				"kind":              uint8(metadb.ConversationKindNormal),
				"channel_id":        "g1",
				"channel_type":      int64(2),
				"read_seq":          uint64(7),
				"deleted_to_seq":    uint64(8),
				"active_at":         int64(100),
				"updated_at":        int64(101),
				"message_seq":       uint64(11),
				"sparse_active":     true,
				"sparse_active_set": true,
			}},
		},
	}, got)
}

func TestDecodeCommandInspectionIncludesConversationKindAndSparseActive(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeUpsertConversationStatesCommand([]metadb.ConversationState{{
		UID:          "u1",
		Kind:         metadb.ConversationKindNormal,
		ChannelID:    "g1",
		ChannelType:  2,
		ReadSeq:      3,
		DeletedToSeq: 4,
		ActiveAt:     5,
		UpdatedAt:    6,
		SparseActive: true,
	}}))
	require.NoError(t, err)

	require.Equal(t, CommandInspection{
		Type: "upsert_conversation_states",
		Payload: map[string]any{
			"command": "upsert_conversation_states",
			"states": []map[string]any{{
				"uid":            "u1",
				"kind":           uint8(metadb.ConversationKindNormal),
				"channel_id":     "g1",
				"channel_type":   int64(2),
				"read_seq":       uint64(3),
				"deleted_to_seq": uint64(4),
				"active_at":      int64(5),
				"updated_at":     int64(6),
				"sparse_active":  true,
			}},
		},
	}, got)
}

func TestEncodeConversationCommandsCheckedRejectInvalidRows(t *testing.T) {
	_, err := EncodeUpsertConversationStatesCommandChecked([]metadb.ConversationState{{
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "g1",
		ChannelType: 2,
	}})
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)

	_, err = EncodeTouchConversationActiveAtCommandChecked([]metadb.ConversationActivePatch{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelType: 2,
	}})
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)

	_, err = EncodeHideConversationsCommandChecked([]metadb.ConversationDelete{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "g1",
		ChannelType: 0,
	}})
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
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

func TestDecodeCommandInspectionIncludesHideConversationKind(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeHideConversationsCommand([]metadb.ConversationDelete{{
		UID:          "u1",
		Kind:         metadb.ConversationKindNormal,
		ChannelID:    "g1",
		ChannelType:  2,
		DeletedToSeq: 10,
		UpdatedAt:    20,
	}}))
	require.NoError(t, err)

	require.Equal(t, CommandInspection{
		Type: "hide_conversations",
		Payload: map[string]any{
			"command": "hide_conversations",
			"deletes": []map[string]any{{
				"uid":            "u1",
				"kind":           uint8(metadb.ConversationKindNormal),
				"channel_id":     "g1",
				"channel_type":   int64(2),
				"deleted_to_seq": uint64(10),
				"updated_at":     int64(20),
			}},
		},
	}, got)
}

func TestDecodeCommandInspectionIncludesMessageEventWithoutPayload(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeAppendMessageEventCommand(metadb.MessageEventAppend{
		ChannelID:   "g1",
		ChannelType: 2,
		ClientMsgNo: "cmn-1",
		EventID:     "evt-1",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamDelta,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  10,
		Payload:     []byte(`{"kind":"text","delta":"secret"}`),
		UpdatedAt:   11,
	}))
	require.NoError(t, err)

	require.Equal(t, CommandInspection{
		Type: "append_message_event",
		Payload: map[string]any{
			"command":       "append_message_event",
			"channel_id":    "g1",
			"channel_type":  int64(2),
			"client_msg_no": "cmn-1",
			"event_id":      "evt-1",
			"event_key":     "main",
			"event_type":    metadb.EventTypeStreamDelta,
			"visibility":    metadb.VisibilityPublic,
			"occurred_at":   int64(10),
			"updated_at":    int64(11),
			"payload_bytes": len([]byte(`{"kind":"text","delta":"secret"}`)),
		},
	}, got)
}

func TestDecodeCommandInspectionIncludesMessageEventBatchWithoutPayloads(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeAppendMessageEventsCommand([]metadb.MessageEventAppend{
		{
			ChannelID:   "g1",
			ChannelType: 2,
			ClientMsgNo: "cmn-1",
			EventID:     "evt-close-main",
			EventKey:    "main",
			EventType:   metadb.EventTypeStreamClose,
			Visibility:  metadb.VisibilityPublic,
			OccurredAt:  10,
			Payload:     []byte(`{"snapshot":{"kind":"text","text":"secret"}}`),
			UpdatedAt:   11,
		},
		{
			ChannelID:   "g1",
			ChannelType: 2,
			ClientMsgNo: "cmn-1",
			EventID:     "evt-finish",
			EventType:   metadb.EventTypeStreamFinish,
			Visibility:  metadb.VisibilityPublic,
			OccurredAt:  12,
			UpdatedAt:   13,
		},
	}))
	require.NoError(t, err)

	require.Equal(t, CommandInspection{
		Type: "append_message_events_batch",
		Payload: map[string]any{
			"command": "append_message_events_batch",
			"events": []map[string]any{
				{
					"channel_id":    "g1",
					"channel_type":  int64(2),
					"client_msg_no": "cmn-1",
					"event_id":      "evt-close-main",
					"event_key":     "main",
					"event_type":    metadb.EventTypeStreamClose,
					"visibility":    metadb.VisibilityPublic,
					"occurred_at":   int64(10),
					"updated_at":    int64(11),
					"payload_bytes": len([]byte(`{"snapshot":{"kind":"text","text":"secret"}}`)),
				},
				{
					"channel_id":    "g1",
					"channel_type":  int64(2),
					"client_msg_no": "cmn-1",
					"event_id":      "evt-finish",
					"event_key":     "",
					"event_type":    metadb.EventTypeStreamFinish,
					"visibility":    metadb.VisibilityPublic,
					"occurred_at":   int64(12),
					"updated_at":    int64(13),
					"payload_bytes": 0,
				},
			},
		},
	}, got)
}

func TestDecodeCommandInspectionIncludesCMDKindConversationCommands(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeUpsertConversationStatesCommand([]metadb.ConversationState{{
		UID:          "u1",
		Kind:         metadb.ConversationKindCMD,
		ChannelID:    "g1____cmd",
		ChannelType:  2,
		ReadSeq:      3,
		DeletedToSeq: 4,
		ActiveAt:     5,
		UpdatedAt:    6,
	}}))
	require.NoError(t, err)
	require.Equal(t, CommandInspection{
		Type: "upsert_conversation_states",
		Payload: map[string]any{
			"command": "upsert_conversation_states",
			"states": []map[string]any{{
				"uid":            "u1",
				"kind":           uint8(metadb.ConversationKindCMD),
				"channel_id":     "g1____cmd",
				"channel_type":   int64(2),
				"read_seq":       uint64(3),
				"deleted_to_seq": uint64(4),
				"active_at":      int64(5),
				"updated_at":     int64(6),
				"sparse_active":  false,
			}},
		},
	}, got)

	got, err = DecodeCommandInspection(EncodeTouchConversationActiveAtCommand([]metadb.ConversationActivePatch{{
		UID:         "u1",
		Kind:        metadb.ConversationKindCMD,
		ChannelID:   "g1____cmd",
		ChannelType: 2,
		ReadSeq:     9,
		UpdatedAt:   10,
	}}))
	require.NoError(t, err)
	require.Equal(t, CommandInspection{
		Type: "touch_conversation_active_at",
		Payload: map[string]any{
			"command": "touch_conversation_active_at",
			"patches": []map[string]any{{
				"uid":               "u1",
				"kind":              uint8(metadb.ConversationKindCMD),
				"channel_id":        "g1____cmd",
				"channel_type":      int64(2),
				"read_seq":          uint64(9),
				"deleted_to_seq":    uint64(0),
				"active_at":         int64(0),
				"updated_at":        int64(10),
				"message_seq":       uint64(0),
				"sparse_active":     false,
				"sparse_active_set": false,
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
				"large":          int64(0),
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
