package fsm

import metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"

// userChannelMembershipPayload describes encoded input, not post-apply state.
func userChannelMembershipPayload(membership metadb.UserChannelMembership) map[string]any {
	return map[string]any{
		"uid":                             membership.UID,
		"channel_id":                      membership.ChannelID,
		"channel_type":                    membership.ChannelType,
		"join_seq":                        membership.JoinSeq,
		"read_seq":                        membership.ReadSeq,
		"deleted_to_seq":                  membership.DeletedToSeq,
		"conversation_hidden_through_seq": membership.ConversationHiddenThroughSeq,
		"activated_at":                    membership.ActivatedAt,
		"tombstone":                       membership.Tombstone,
		"tombstone_at":                    membership.TombstoneAt,
		"source_version":                  membership.SourceVersion,
		"updated_at":                      membership.UpdatedAt,
	}
}

func userChannelMembershipsInspection(commandType string, memberships []metadb.UserChannelMembership) CommandInspection {
	items := make([]map[string]any, len(memberships))
	for i, membership := range memberships {
		items[i] = userChannelMembershipPayload(membership)
	}
	return simpleInspection(commandType, map[string]any{"memberships": items})
}

func userCMDChannelMembershipsInspection(commandType string, memberships []metadb.UserCMDChannelMembership) CommandInspection {
	items := make([]map[string]any, len(memberships))
	for i, membership := range memberships {
		items[i] = map[string]any{
			"uid":                membership.UID,
			"command_channel_id": membership.CommandChannelID,
			"channel_type":       membership.ChannelType,
			"start_seq":          membership.StartSeq,
			"ack_seq":            membership.AckSeq,
			"tombstone":          membership.Tombstone,
			"tombstone_at":       membership.TombstoneAt,
			"updated_at":         membership.UpdatedAt,
		}
	}
	return simpleInspection(commandType, map[string]any{"memberships": items})
}

// channelLatestPayload omits message bodies from the operator log view.
func channelLatestPayload(latest metadb.ChannelLatest) map[string]any {
	return map[string]any{
		"channel_id":       latest.ChannelID,
		"channel_type":     latest.ChannelType,
		"last_message_id":  latest.LastMessageID,
		"last_message_seq": latest.LastMessageSeq,
		"last_at":          latest.LastAt,
		"from_uid":         latest.FromUID,
		"client_msg_no":    latest.ClientMsgNo,
		"payload_bytes":    len(latest.Payload),
		"updated_at":       latest.UpdatedAt,
	}
}
