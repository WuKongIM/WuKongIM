package fsm

import (
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

const redactedSecret = "***"

// CommandInspection is a redacted, JSON-friendly view of one Slot FSM command.
type CommandInspection struct {
	// Type is the stable machine-readable command type.
	Type string
	// Payload contains a JSON-friendly command summary with sensitive fields redacted.
	Payload map[string]any
}

// DecodeCommandInspection decodes one Slot FSM command into a redacted summary.
func DecodeCommandInspection(data []byte) (CommandInspection, error) {
	cmd, err := decodeCommand(data)
	if err != nil {
		return CommandInspection{}, err
	}
	return inspectCommand(cmd)
}

func inspectCommand(cmd command) (CommandInspection, error) {
	switch typed := cmd.(type) {
	case *upsertUserCmd:
		return userInspection("upsert_user", typed.user), nil
	case *createUserCmd:
		return userInspection("create_user", typed.user), nil
	case *upsertDeviceCmd:
		return deviceInspection("upsert_device", typed.device), nil
	case *upsertChannelCmd:
		return channelInspection("upsert_channel", typed.channel), nil
	case *deleteChannelCmd:
		return simpleInspection("delete_channel", map[string]any{
			"channel_id":   typed.channelID,
			"channel_type": typed.channelType,
		}), nil
	case *upsertChannelRuntimeMetaCmd:
		return runtimeMetaInspection("upsert_channel_runtime_meta", typed.meta), nil
	case *deleteChannelRuntimeMetaCmd:
		return simpleInspection("delete_channel_runtime_meta", map[string]any{
			"channel_id":   typed.channelID,
			"channel_type": typed.channelType,
		}), nil
	case *addSubscribersCmd:
		return subscribersInspection("add_subscribers", typed.channelID, typed.channelType, typed.uids), nil
	case *removeSubscribersCmd:
		return subscribersInspection("remove_subscribers", typed.channelID, typed.channelType, typed.uids), nil
	case *upsertUserConversationStatesCmd:
		return simpleInspection("upsert_user_conversation_states", map[string]any{
			"states": userConversationStatesPayload(typed.states),
		}), nil
	case *touchUserConversationActiveAtCmd:
		return simpleInspection("touch_user_conversation_active_at", map[string]any{
			"patches": userConversationActivePatchesPayload(typed.patches),
		}), nil
	case *clearUserConversationActiveAtCmd:
		return simpleInspection("clear_user_conversation_active_at", map[string]any{
			"uid":  typed.uid,
			"keys": conversationKeysPayload(typed.keys),
		}), nil
	case *upsertChannelUpdateLogsCmd:
		return simpleInspection("upsert_channel_update_logs", map[string]any{
			"entries": channelUpdateLogsPayload(typed.entries),
		}), nil
	case *deleteChannelUpdateLogsCmd:
		return simpleInspection("delete_channel_update_logs", map[string]any{
			"keys": conversationKeysPayload(typed.keys),
		}), nil
	case *applyDeltaCmd:
		return applyDeltaInspection(typed)
	case *enterFenceCmd:
		return simpleInspection("enter_fence", map[string]any{
			"hash_slot": typed.HashSlot,
			"target":    typed.Target,
		}), nil
	case *ackMigrationOutboxCmd:
		return simpleInspection("ack_migration_outbox", map[string]any{
			"hash_slot":    typed.HashSlot,
			"source_slot":  typed.SourceSlot,
			"target_slot":  typed.TargetSlot,
			"source_index": typed.SourceIndex,
		}), nil
	case *cleanupMigrationOutboxCmd:
		return simpleInspection("cleanup_migration_outbox", map[string]any{
			"hash_slot":     typed.HashSlot,
			"source_slot":   typed.SourceSlot,
			"target_slot":   typed.TargetSlot,
			"through_index": typed.ThroughIndex,
		}), nil
	default:
		return CommandInspection{}, fmt.Errorf("%w: unsupported command inspection %T", metadb.ErrInvalidArgument, cmd)
	}
}

func simpleInspection(commandType string, payload map[string]any) CommandInspection {
	payload["command"] = commandType
	return CommandInspection{Type: commandType, Payload: payload}
}

func userInspection(commandType string, user metadb.User) CommandInspection {
	return simpleInspection(commandType, map[string]any{
		"uid":          user.UID,
		"token":        redactedSecret,
		"device_flag":  user.DeviceFlag,
		"device_level": user.DeviceLevel,
	})
}

func deviceInspection(commandType string, device metadb.Device) CommandInspection {
	return simpleInspection(commandType, map[string]any{
		"uid":          device.UID,
		"device_flag":  device.DeviceFlag,
		"token":        redactedSecret,
		"device_level": device.DeviceLevel,
	})
}

func channelInspection(commandType string, channel metadb.Channel) CommandInspection {
	return simpleInspection(commandType, map[string]any{
		"channel_id":   channel.ChannelID,
		"channel_type": channel.ChannelType,
		"ban":          channel.Ban,
	})
}

func runtimeMetaInspection(commandType string, meta metadb.ChannelRuntimeMeta) CommandInspection {
	return simpleInspection(commandType, map[string]any{
		"channel_id":     meta.ChannelID,
		"channel_type":   meta.ChannelType,
		"channel_epoch":  meta.ChannelEpoch,
		"leader_epoch":   meta.LeaderEpoch,
		"replicas":       append([]uint64(nil), meta.Replicas...),
		"isr":            append([]uint64(nil), meta.ISR...),
		"leader":         meta.Leader,
		"min_isr":        meta.MinISR,
		"status":         meta.Status,
		"features":       meta.Features,
		"lease_until_ms": meta.LeaseUntilMS,
	})
}

func subscribersInspection(commandType, channelID string, channelType int64, uids []string) CommandInspection {
	return simpleInspection(commandType, map[string]any{
		"channel_id":   channelID,
		"channel_type": channelType,
		"uids":         append([]string(nil), uids...),
	})
}

func applyDeltaInspection(cmd *applyDeltaCmd) (CommandInspection, error) {
	original, err := DecodeCommandInspection(cmd.OriginalCmd)
	if err != nil {
		return CommandInspection{}, err
	}
	return simpleInspection("apply_delta", map[string]any{
		"source_slot_id": cmd.SourceSlotID,
		"source_index":   cmd.SourceIndex,
		"hash_slot":      cmd.HashSlot,
		"original":       original.Payload,
	}), nil
}

func userConversationStatesPayload(states []metadb.UserConversationState) []map[string]any {
	out := make([]map[string]any, 0, len(states))
	for _, state := range states {
		out = append(out, map[string]any{
			"uid":            state.UID,
			"channel_id":     state.ChannelID,
			"channel_type":   state.ChannelType,
			"read_seq":       state.ReadSeq,
			"deleted_to_seq": state.DeletedToSeq,
			"active_at":      state.ActiveAt,
			"updated_at":     state.UpdatedAt,
		})
	}
	return out
}

func userConversationActivePatchesPayload(patches []metadb.UserConversationActivePatch) []map[string]any {
	out := make([]map[string]any, 0, len(patches))
	for _, patch := range patches {
		out = append(out, map[string]any{
			"uid":          patch.UID,
			"channel_id":   patch.ChannelID,
			"channel_type": patch.ChannelType,
			"active_at":    patch.ActiveAt,
		})
	}
	return out
}

func conversationKeysPayload(keys []metadb.ConversationKey) []map[string]any {
	out := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		out = append(out, map[string]any{
			"channel_id":   key.ChannelID,
			"channel_type": key.ChannelType,
		})
	}
	return out
}

func channelUpdateLogsPayload(entries []metadb.ChannelUpdateLog) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		out = append(out, map[string]any{
			"channel_id":         entry.ChannelID,
			"channel_type":       entry.ChannelType,
			"updated_at":         entry.UpdatedAt,
			"last_msg_seq":       entry.LastMsgSeq,
			"last_client_msg_no": entry.LastClientMsgNo,
			"last_msg_at":        entry.LastMsgAt,
		})
	}
	return out
}
