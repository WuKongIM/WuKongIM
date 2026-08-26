package app

import (
	"context"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
)

// conversationLegacyMessageReader adapts the message use case to the narrow
// legacy conversation-sync message port without coupling sibling use cases.
type conversationLegacyMessageReader struct {
	messages *messageusecase.App
}

func (r conversationLegacyMessageReader) ReadLegacyMessagesBatch(ctx context.Context, uid string, queries []conversationusecase.LegacyMessageQuery) ([]conversationusecase.LegacyMessageReadResult, error) {
	items := make([]messageusecase.SyncChannelMessagesQuery, len(queries))
	for index, query := range queries {
		items[index] = messageusecase.SyncChannelMessagesQuery{
			ChannelID: query.ChannelID, ChannelType: query.ChannelType,
			StartMessageSeq: 0, EndMessageSeq: query.AfterMessageSeq,
			Limit: query.Limit, PullMode: messageusecase.PullModeDown,
			IncludeEventMeta: true,
		}
	}
	batch, err := r.messages.SyncChannelMessagesBatch(ctx, messageusecase.SyncChannelMessagesBatchQuery{
		LoginUID: uid,
		Items:    items,
	})
	if err != nil {
		return nil, err
	}
	results := make([]conversationusecase.LegacyMessageReadResult, len(batch.Items))
	for index, item := range batch.Items {
		result := &results[index]
		result.ChannelID = item.ChannelID
		result.ChannelType = item.ChannelType
		result.Err = item.Err
		result.Messages = make([]conversationusecase.LegacyRecentMessage, 0, len(item.Result.Messages))
		for _, msg := range item.Result.Messages {
			result.Messages = append(result.Messages, conversationusecase.LegacyRecentMessage{
				Flags: conversationusecase.LegacyMessageFlags{
					NoPersist: msg.Flags.NoPersist,
					RedDot:    msg.Flags.RedDot,
					SyncOnce:  msg.Flags.SyncOnce,
				},
				Setting: msg.Setting, MessageID: msg.MessageID, ClientMsgNo: msg.ClientMsgNo,
				MessageSeq: msg.MessageSeq, FromUID: msg.FromUID, ChannelID: msg.ChannelID,
				ChannelType: msg.ChannelType, Topic: msg.Topic, Expire: msg.Expire,
				Timestamp: msg.Timestamp, Payload: append([]byte(nil), msg.Payload...),
				End: msg.End, EndReason: msg.EndReason, Error: msg.Error,
				StreamData: append([]byte(nil), msg.StreamData...),
				EventMeta:  conversationLegacyEventMeta(msg.EventMeta),
				EventHint:  conversationLegacyEventHint(msg.EventHint),
			})
		}
	}
	return results, nil
}

func conversationLegacyEventMeta(meta *messageusecase.MessageEventMeta) *conversationusecase.LegacyMessageEventMeta {
	if meta == nil {
		return nil
	}
	result := &conversationusecase.LegacyMessageEventMeta{
		HasEvents: meta.HasEvents, Completed: meta.Completed, EventVersion: meta.EventVersion,
		LastMsgEventSeq: meta.LastMsgEventSeq, EventCount: meta.EventCount,
		OpenEventCount: meta.OpenEventCount,
		Events:         make([]conversationusecase.LegacyMessageEventKeyMeta, 0, len(meta.Events)),
	}
	for _, event := range meta.Events {
		result.Events = append(result.Events, conversationusecase.LegacyMessageEventKeyMeta{
			EventKey: event.EventKey, Status: event.Status, LastMsgEventSeq: event.LastMsgEventSeq,
			EndReason: event.EndReason, Error: event.Error, Snapshot: event.Snapshot,
		})
	}
	return result
}

func conversationLegacyEventHint(hint *messageusecase.MessageEventSyncHint) *conversationusecase.LegacyMessageEventSyncHint {
	if hint == nil {
		return nil
	}
	return &conversationusecase.LegacyMessageEventSyncHint{
		ClientMsgNo: hint.ClientMsgNo, FromMsgEventSeq: hint.FromMsgEventSeq,
	}
}
