package fsm

import (
	"errors"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const redactedSecret = "***"

// ErrCommandInspectionUnsupported means the command version, type, or decoded
// command has no inspection view. It does not establish whether the data is corrupt.
var ErrCommandInspectionUnsupported = errors.New("unsupported command inspection")

// CommandInspection is a redacted, JSON-friendly view of one Slot FSM command.
type CommandInspection struct {
	// Type is the stable machine-readable command type.
	Type string
	// Payload contains a JSON-friendly command summary with sensitive fields redacted.
	Payload map[string]any
}

// DecodeCommandInspection decodes one Slot FSM command into a redacted summary.
func DecodeCommandInspection(data []byte) (CommandInspection, error) {
	// Classify unfamiliar wire formats only for inspection; the FSM decoder
	// continues to reject them under its existing application contract.
	if len(data) >= headerSize {
		if data[0] != commandVersion {
			return CommandInspection{}, fmt.Errorf("%w: command version %d", ErrCommandInspectionUnsupported, data[0])
		}
		if _, ok := commandDecoders[data[1]]; !ok {
			return CommandInspection{}, fmt.Errorf("%w: command type %d", ErrCommandInspectionUnsupported, data[1])
		}
	}
	cmd, err := decodeCommand(data)
	if err != nil {
		return CommandInspection{}, err
	}
	return inspectCommand(cmd)
}

func inspectCommand(cmd command) (CommandInspection, error) {
	switch typed := cmd.(type) {
	case *messageUpdateCmd:
		q := typed.mutation
		return simpleInspection("message_update", map[string]any{"operation": q.Op, "channel_id": q.ChannelID, "channel_type": q.ChannelType, "message_id": q.MessageID, "message_seq": q.MessageSeq, "expected_version": q.ExpectedVersion, "payload_bytes": len(q.Payload)}), nil
	case *noopCmd:
		return simpleInspection("noop", map[string]any{"command": "noop"}), nil
	case *upsertUserCmd:
		return userInspection("upsert_user", typed.user), nil
	case *createUserCmd:
		return userInspection("create_user", typed.user), nil
	case *upsertDeviceCmd:
		return deviceInspection("upsert_device", typed.device), nil
	case *upsertChannelCmd:
		return channelInspection("upsert_channel", typed.channel), nil
	case *createChannelCmd:
		return channelInspection("create_channel", typed.channel), nil
	case *patchChannelBusinessFlagsCmd:
		return simpleInspection("patch_channel_business_flags", map[string]any{
			"channel_id": typed.channelID, "channel_type": typed.channelType,
			"ban": typed.flags.Ban, "disband": typed.flags.Disband, "send_ban": typed.flags.SendBan,
		}), nil
	case *deleteChannelCmd:
		return simpleInspection("delete_channel", map[string]any{
			"channel_id":   typed.channelID,
			"channel_type": typed.channelType,
		}), nil
	case *upsertChannelRuntimeMetaCmd:
		return runtimeMetaInspection("upsert_channel_runtime_meta", typed.meta), nil
	case *createChannelRuntimeMetaBatchCmd:
		items := make([]map[string]any, len(typed.items))
		for i, item := range typed.items {
			items[i] = runtimeMetaInspection("create_channel_runtime_meta", item.Meta).Payload
			items[i]["hash_slot"] = item.HashSlot
		}
		return simpleInspection("create_channel_runtime_meta_batch", map[string]any{"items": items}), nil
	case *admitPersonDirectoryTaskBatchCmd:
		items := make([]map[string]any, len(typed.items))
		for i, item := range typed.items {
			items[i] = map[string]any{
				"hash_slot":      item.HashSlot,
				"channel_id":     item.Task.ChannelID,
				"channel_type":   item.Task.ChannelType,
				"committed_tail": item.Task.CommittedTail,
				"created_at":     item.Task.CreatedAt,
				"runtime_meta":   runtimeMetaInspection("create_channel_runtime_meta", item.RuntimeMeta).Payload,
			}
		}
		return simpleInspection("admit_person_directory_task_batch", map[string]any{"items": items}), nil
	case *ensureUserChannelMembershipBatchCmd:
		items := make([]map[string]any, len(typed.items))
		for i, item := range typed.items {
			items[i] = userChannelMembershipPayload(item.Membership)
			items[i]["hash_slot"] = item.HashSlot
		}
		return simpleInspection("ensure_user_channel_membership_batch", map[string]any{"items": items}), nil
	case *completePersonDirectoryTaskBatchCmd:
		items := make([]map[string]any, len(typed.items))
		for i, item := range typed.items {
			items[i] = map[string]any{
				"hash_slot":    item.HashSlot,
				"channel_id":   item.ChannelID,
				"channel_type": item.ChannelType,
				"generation":   item.Generation,
			}
		}
		return simpleInspection("complete_person_directory_task_batch", map[string]any{"items": items}), nil
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
	case *upsertUserChannelMembershipsCmd:
		return userChannelMembershipsInspection("upsert_user_channel_memberships", typed.memberships), nil
	case *deleteUserChannelMembershipsCmd:
		return userChannelMembershipsInspection("delete_user_channel_memberships", typed.memberships), nil
	case *advanceUserChannelMembershipReadSeqCmd:
		return userChannelMembershipsInspection("advance_user_channel_membership_read_seq", typed.memberships), nil
	case *hideUserChannelMembershipCmd:
		return userChannelMembershipsInspection("hide_user_channel_membership", typed.memberships), nil
	case *activateUserChannelMembershipCmd:
		return userChannelMembershipsInspection("activate_user_channel_membership", typed.memberships), nil
	case *upsertUserCMDChannelMembershipsCmd:
		return userCMDChannelMembershipsInspection("upsert_user_cmd_channel_memberships", typed.memberships), nil
	case *advanceUserCMDChannelMembershipAcksCmd:
		return userCMDChannelMembershipsInspection("advance_user_cmd_channel_membership_acks", typed.memberships), nil
	case *tombstoneUserCMDChannelMembershipsCmd:
		return userCMDChannelMembershipsInspection("tombstone_user_cmd_channel_memberships", typed.memberships), nil
	case *upsertChannelLatestCmd:
		return simpleInspection("upsert_channel_latest", channelLatestPayload(typed.latest)), nil
	case *upsertChannelLatestBatchCmd:
		items := make([]map[string]any, len(typed.items))
		for i, item := range typed.items {
			items[i] = channelLatestPayload(item.Latest)
			items[i]["hash_slot"] = item.HashSlot
		}
		return simpleInspection("upsert_channel_latest_batch", map[string]any{"items": items}), nil
	case *appendMessageEventCmd:
		return simpleInspection("append_message_event", messageEventAppendPayload(typed.event)), nil
	case *appendMessageEventsBatchCmd:
		return simpleInspection("append_message_events_batch", map[string]any{
			"events": messageEventAppendBatchPayload(typed.events),
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
	case *garbageCollectMigrationTasksCmd:
		return simpleInspection("garbage_collect_terminal_channel_migration_tasks", map[string]any{
			"before_ms": typed.req.BeforeMS, "limit": typed.req.Limit,
		}), nil
	default:
		return CommandInspection{}, fmt.Errorf("%w %T", ErrCommandInspectionUnsupported, cmd)
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
		"large":          channel.Large,
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

func messageEventAppendBatchPayload(events []metadb.MessageEventAppend) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for _, event := range events {
		out = append(out, messageEventAppendPayload(event))
	}
	return out
}

func messageEventAppendPayload(event metadb.MessageEventAppend) map[string]any {
	return map[string]any{
		"channel_id":    event.ChannelID,
		"channel_type":  event.ChannelType,
		"client_msg_no": event.ClientMsgNo,
		"event_id":      event.EventID,
		"event_key":     event.EventKey,
		"event_type":    event.EventType,
		"visibility":    event.Visibility,
		"occurred_at":   event.OccurredAt,
		"updated_at":    event.UpdatedAt,
		"payload_bytes": len(event.Payload),
	}
}

func conversationKeysPayload(keys []metadb.ChannelKey) []map[string]any {
	out := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		out = append(out, map[string]any{
			"channel_id":   key.ChannelID,
			"channel_type": key.ChannelType,
		})
	}
	return out
}
