package fsm

import (
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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
	case *advanceChannelRetentionThroughSeqCmd:
		return retentionAdvanceInspection(typed.req), nil
	case *addSubscribersCmd:
		return subscribersInspection("add_subscribers", typed.channelID, typed.channelType, typed.uids, typed.subscriberMutationVersion), nil
	case *removeSubscribersCmd:
		return subscribersInspection("remove_subscribers", typed.channelID, typed.channelType, typed.uids, typed.subscriberMutationVersion), nil
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
	case *reservedConversationProjectionCmd:
		return simpleInspection("reserved_conversation_projection", map[string]any{
			"deprecated": true,
			"reserved":   true,
		}), nil
	case *hideUserConversationsCmd:
		return simpleInspection("hide_user_conversations", map[string]any{
			"deletes": userConversationDeletesPayload(typed.deletes),
		}), nil
	case *upsertCMDConversationStatesCmd:
		return simpleInspection("upsert_cmd_conversation_states", map[string]any{
			"states": cmdConversationStatesPayload(typed.states),
		}), nil
	case *advanceCMDConversationReadSeqCmd:
		return simpleInspection("advance_cmd_conversation_read_seq", map[string]any{
			"patches": cmdConversationReadPatchesPayload(typed.patches),
		}), nil
	case *bindPluginUserCmd:
		return pluginBindingInspection("bind_plugin_user", typed.binding), nil
	case *unbindPluginUserCmd:
		return simpleInspection("unbind_plugin_user", map[string]any{
			"uid":       typed.uid,
			"plugin_no": typed.pluginNo,
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
	case *createChannelMigrationTaskCmd:
		return channelMigrationTaskInspection("create_channel_migration_task", typed.task), nil
	case *createChannelMigrationTaskWithRuntimeGuardCmd:
		return channelMigrationTaskInspection("create_channel_migration_task_with_runtime_guard", typed.req.Task), nil
	case *claimChannelMigrationTaskCmd:
		return channelMigrationGuardInspection("claim_channel_migration_task", typed.req.Guard), nil
	case *advanceChannelMigrationTaskCmd:
		return channelMigrationGuardInspection("advance_channel_migration_task", typed.req.Guard), nil
	case *setChannelWriteFenceCmd:
		return channelMigrationGuardInspection("set_channel_write_fence", typed.req.Guard), nil
	case *resetChannelWriteFenceToPreCutoverCmd:
		return channelMigrationGuardInspection("reset_channel_write_fence", typed.req.Guard), nil
	case *commitChannelLeaderTransferCmd:
		return channelMigrationGuardInspection("commit_channel_leader_transfer", typed.req.Guard), nil
	case *addChannelLearnerCmd:
		return channelMigrationGuardInspection("add_channel_learner", typed.req.Guard), nil
	case *promoteLearnerAndRemoveReplicaCmd:
		return channelMigrationGuardInspection("promote_learner_and_remove_replica", typed.req.Guard), nil
	case *clearChannelWriteFenceCmd:
		return channelMigrationGuardInspection("clear_channel_write_fence", typed.req.Guard), nil
	case *abortChannelMigrationCmd:
		return channelMigrationGuardInspection("abort_channel_migration", typed.req.Guard), nil
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
		"channel_id":     channel.ChannelID,
		"channel_type":   channel.ChannelType,
		"ban":            channel.Ban,
		"disband":        channel.Disband,
		"send_ban":       channel.SendBan,
		"allow_stranger": channel.AllowStranger,
	})
}

func runtimeMetaInspection(commandType string, meta metadb.ChannelRuntimeMeta) CommandInspection {
	return simpleInspection(commandType, map[string]any{
		"channel_id":              meta.ChannelID,
		"channel_type":            meta.ChannelType,
		"channel_epoch":           meta.ChannelEpoch,
		"leader_epoch":            meta.LeaderEpoch,
		"replicas":                append([]uint64(nil), meta.Replicas...),
		"isr":                     append([]uint64(nil), meta.ISR...),
		"leader":                  meta.Leader,
		"min_isr":                 meta.MinISR,
		"status":                  meta.Status,
		"features":                meta.Features,
		"lease_until_ms":          meta.LeaseUntilMS,
		"retention_through_seq":   meta.RetentionThroughSeq,
		"retention_updated_at_ms": meta.RetentionUpdatedAtMS,
		"write_fence_token":       meta.WriteFenceToken,
		"write_fence_version":     meta.WriteFenceVersion,
		"write_fence_reason":      meta.WriteFenceReason,
		"write_fence_until_ms":    meta.WriteFenceUntilMS,
		"route_generation":        meta.RouteGeneration,
	})
}

func retentionAdvanceInspection(req metadb.ChannelRetentionAdvance) CommandInspection {
	return simpleInspection("advance_channel_retention_through_seq", map[string]any{
		"channel_id":              req.ChannelID,
		"channel_type":            req.ChannelType,
		"expected_channel_epoch":  req.ExpectedChannelEpoch,
		"expected_leader_epoch":   req.ExpectedLeaderEpoch,
		"expected_leader":         req.ExpectedLeader,
		"expected_lease_until_ms": req.ExpectedLeaseUntilMS,
		"retention_through_seq":   req.RetentionThroughSeq,
		"retention_updated_at_ms": req.RetentionUpdatedAtMS,
	})
}

func subscribersInspection(commandType, channelID string, channelType int64, uids []string, subscriberMutationVersion uint64) CommandInspection {
	payload := map[string]any{
		"channel_id":   channelID,
		"channel_type": channelType,
		"uids":         append([]string(nil), uids...),
	}
	if subscriberMutationVersion > 0 {
		payload["subscriber_mutation_version"] = subscriberMutationVersion
	}
	return simpleInspection(commandType, payload)
}

func channelMigrationTaskInspection(commandType string, task metadb.ChannelMigrationTask) CommandInspection {
	return simpleInspection(commandType, map[string]any{
		"task_id":      task.TaskID,
		"kind":         task.Kind,
		"status":       task.Status,
		"phase":        task.Phase,
		"channel_id":   task.ChannelID,
		"channel_type": task.ChannelType,
		"source_node":  task.SourceNode,
		"target_node":  task.TargetNode,
	})
}

func channelMigrationGuardInspection(commandType string, guard metadb.ChannelMigrationTaskGuard) CommandInspection {
	return simpleInspection(commandType, map[string]any{
		"task_id":         guard.TaskID,
		"channel_id":      guard.ChannelID,
		"channel_type":    guard.ChannelType,
		"expected_status": guard.ExpectedStatus,
		"expected_phase":  guard.ExpectedPhase,
	})
}

func pluginBindingInspection(commandType string, binding metadb.PluginUserBinding) CommandInspection {
	return simpleInspection(commandType, map[string]any{
		"uid":           binding.UID,
		"plugin_no":     binding.PluginNo,
		"created_at_ms": binding.CreatedAtMS,
		"updated_at_ms": binding.UpdatedAtMS,
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
			"message_seq":  patch.MessageSeq,
		})
	}
	return out
}

func userConversationDeletesPayload(deletes []metadb.UserConversationDelete) []map[string]any {
	out := make([]map[string]any, 0, len(deletes))
	for _, req := range deletes {
		out = append(out, map[string]any{
			"uid":            req.UID,
			"channel_id":     req.ChannelID,
			"channel_type":   req.ChannelType,
			"deleted_to_seq": req.DeletedToSeq,
			"updated_at":     req.UpdatedAt,
		})
	}
	return out
}

func cmdConversationStatesPayload(states []metadb.CMDConversationState) []map[string]any {
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

func cmdConversationReadPatchesPayload(patches []metadb.CMDConversationReadPatch) []map[string]any {
	out := make([]map[string]any, 0, len(patches))
	for _, patch := range patches {
		out = append(out, map[string]any{
			"uid":          patch.UID,
			"channel_id":   patch.ChannelID,
			"channel_type": patch.ChannelType,
			"read_seq":     patch.ReadSeq,
			"updated_at":   patch.UpdatedAt,
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
