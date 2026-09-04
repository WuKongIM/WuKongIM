package api

import (
	"net/http"
	"strconv"
	"strings"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/gin-gonic/gin"
)

type conversationSyncLegacyRequest struct {
	UID                 string  `json:"uid"`
	Version             int64   `json:"version"`
	LastMessageSeqs     string  `json:"last_msg_seqs"`
	MessageCount        int     `json:"msg_count"`
	OnlyUnread          uint8   `json:"only_unread"`
	ExcludeChannelTypes []uint8 `json:"exclude_channel_types"`
	Page                int     `json:"page"`
	PageSize            int     `json:"page_size"`
}

type conversationSyncLegacyResponse struct {
	ChannelID        string              `json:"channel_id"`
	ChannelType      uint8               `json:"channel_type"`
	Unread           uint64              `json:"unread"`
	Timestamp        int64               `json:"timestamp"`
	LastMessageSeq   uint64              `json:"last_msg_seq"`
	LastClientMsgNo  string              `json:"last_client_msg_no"`
	OffsetMessageSeq int64               `json:"offset_msg_seq"`
	ReadToMessageSeq uint64              `json:"readed_to_msg_seq"`
	Version          int64               `json:"version"`
	Recents          []legacyMessageResp `json:"recents"`
}

func (s *Server) handleConversationSyncLegacy(c *gin.Context) {
	var req conversationSyncLegacyRequest
	if !bindJSON(c, &req) {
		return
	}
	if strings.TrimSpace(req.UID) == "" {
		writeJSONError(c, "uid不能为空！")
		return
	}
	syncer, ok := s.conversations.(conversationusecase.LegacySyncer)
	if !ok {
		writeJSONError(c, "legacy conversation sync usecase not configured")
		return
	}
	result, err := syncer.SyncLegacy(c.Request.Context(), conversationusecase.LegacySyncRequest{
		UID:                   req.UID,
		Version:               req.Version,
		ClientLastMessageSeqs: parseLegacyConversationCursors(req.UID, req.LastMessageSeqs),
		MessageCount:          req.MessageCount,
		OnlyUnread:            req.OnlyUnread == 1,
		ExcludeChannelTypes:   append([]uint8(nil), req.ExcludeChannelTypes...),
		Page:                  req.Page,
		PageSize:              req.PageSize,
	})
	if err != nil {
		writeJSONError(c, err.Error())
		return
	}
	resp := make([]conversationSyncLegacyResponse, 0, len(result.Items))
	for _, item := range result.Items {
		channelID := legacyMessageChannelID(req.UID, item.ChannelID, item.ChannelType)
		if item.ChannelType == frame.ChannelTypePerson && channelID == s.systemUID {
			continue
		}
		row := conversationSyncLegacyResponse{
			ChannelID:        channelID,
			ChannelType:      item.ChannelType,
			Unread:           item.Unread,
			Timestamp:        item.Timestamp,
			LastMessageSeq:   item.LastMessageSeq,
			LastClientMsgNo:  item.LastClientMsgNo,
			OffsetMessageSeq: item.OffsetMessageSeq,
			ReadToMessageSeq: item.ReadToMessageSeq,
			Version:          item.Version,
			Recents:          make([]legacyMessageResp, 0, len(item.Recents)),
		}
		for _, msg := range item.Recents {
			synced := legacyRecentMessageToSynced(msg)
			if synced.FromUID == s.systemUID {
				synced.FromUID = ""
			}
			row.Recents = append(row.Recents, newLegacyMessageResp(req.UID, synced))
		}
		resp = append(resp, row)
	}
	c.JSON(http.StatusOK, resp)
}

func parseLegacyConversationCursors(uid, encoded string) []conversationusecase.LegacyConversationCursor {
	parts := strings.Split(encoded, "|")
	result := make([]conversationusecase.LegacyConversationCursor, 0, len(parts))
	for _, part := range parts {
		fields := strings.Split(part, ":")
		if len(fields) != 3 || strings.TrimSpace(fields[0]) == "" {
			continue
		}
		channelType, err := strconv.ParseUint(fields[1], 10, 8)
		if err != nil || channelType == 0 {
			continue
		}
		messageSeq, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			messageSeq = 0
		}
		channelID := fields[0]
		if channelType == uint64(frame.ChannelTypePerson) {
			normalized, err := runtimechannelid.NormalizePersonChannel(uid, channelID)
			if err != nil {
				continue
			}
			channelID = normalized
		}
		result = append(result, conversationusecase.LegacyConversationCursor{
			ChannelID: channelID, ChannelType: uint8(channelType), LastMessageSeq: messageSeq,
		})
	}
	return result
}

func legacyRecentMessageToSynced(msg conversationusecase.LegacyRecentMessage) messageusecase.SyncedMessage {
	return messageusecase.SyncedMessage{
		Flags: messageusecase.MessageFlags{
			NoPersist: msg.Flags.NoPersist,
			RedDot:    msg.Flags.RedDot,
			SyncOnce:  msg.Flags.SyncOnce,
		},
		Setting: msg.Setting, MessageID: msg.MessageID, ClientMsgNo: msg.ClientMsgNo,
		MessageSeq: msg.MessageSeq, FromUID: msg.FromUID, ChannelID: msg.ChannelID,
		ChannelType: msg.ChannelType, Topic: msg.Topic, Expire: msg.Expire,
		Timestamp: msg.Timestamp, Payload: msg.Payload, End: msg.End,
		EndReason: msg.EndReason, Error: msg.Error, StreamData: msg.StreamData,
		EventMeta: legacyMessageEventMetaToSynced(msg.EventMeta),
		EventHint: legacyMessageEventHintToSynced(msg.EventHint),
	}
}

func legacyMessageEventMetaToSynced(meta *conversationusecase.LegacyMessageEventMeta) *messageusecase.MessageEventMeta {
	if meta == nil {
		return nil
	}
	result := &messageusecase.MessageEventMeta{
		HasEvents: meta.HasEvents, Completed: meta.Completed, EventVersion: meta.EventVersion,
		LastMsgEventSeq: meta.LastMsgEventSeq, EventCount: meta.EventCount,
		OpenEventCount: meta.OpenEventCount,
		Events:         make([]messageusecase.MessageEventKeyMeta, 0, len(meta.Events)),
	}
	for _, event := range meta.Events {
		result.Events = append(result.Events, messageusecase.MessageEventKeyMeta{
			EventKey: event.EventKey, Status: event.Status, LastMsgEventSeq: event.LastMsgEventSeq,
			EndReason: event.EndReason, Error: event.Error, Snapshot: event.Snapshot,
		})
	}
	return result
}

func legacyMessageEventHintToSynced(hint *conversationusecase.LegacyMessageEventSyncHint) *messageusecase.MessageEventSyncHint {
	if hint == nil {
		return nil
	}
	return &messageusecase.MessageEventSyncHint{
		ClientMsgNo: hint.ClientMsgNo, FromMsgEventSeq: hint.FromMsgEventSeq,
	}
}
