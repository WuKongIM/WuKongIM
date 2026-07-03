package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	messageusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/gin-gonic/gin"
)

type syncConversationRequest struct {
	UID                 string  `json:"uid"`
	Version             int64   `json:"version"`
	LastMsgSeqs         string  `json:"last_msg_seqs"`
	MsgCount            int     `json:"msg_count"`
	OnlyUnread          uint8   `json:"only_unread"`
	ExcludeChannelTypes []uint8 `json:"exclude_channel_types"`
	Limit               int     `json:"limit"`
}

type legacyConversationResponse struct {
	ChannelID       string              `json:"channel_id"`
	ChannelType     uint8               `json:"channel_type"`
	Unread          int                 `json:"unread"`
	Timestamp       int64               `json:"timestamp"`
	LastMsgSeq      uint32              `json:"last_msg_seq"`
	LastClientMsgNo string              `json:"last_client_msg_no"`
	OffsetMsgSeq    int64               `json:"offset_msg_seq"`
	ReadedToMsgSeq  uint32              `json:"readed_to_msg_seq"`
	Version         int64               `json:"version"`
	Recents         []legacyMessageResp `json:"recents,omitempty"`
}

func (s *Server) handleConversationSync(c *gin.Context) {
	start := time.Now()
	var req syncConversationRequest
	if !bindJSON(c, &req) {
		s.observeConversationSync(ConversationSyncObservation{Result: "invalid_request", Duration: time.Since(start)})
		return
	}
	onlyUnread := req.OnlyUnread == 1
	withRecents := req.MsgCount > 0
	if req.UID == "" {
		writeJSONError(c, "invalid request")
		s.observeConversationSync(ConversationSyncObservation{Result: "invalid_request", Duration: time.Since(start), OnlyUnread: onlyUnread, WithRecents: withRecents})
		return
	}
	if s == nil || s.conversations == nil {
		writeJSONError(c, "conversation usecase not configured")
		s.observeConversationSync(ConversationSyncObservation{Result: "not_configured", Duration: time.Since(start), OnlyUnread: onlyUnread, WithRecents: withRecents})
		return
	}
	lastMsgSeqs, err := parseLegacyLastMsgSeqs(req.UID, req.LastMsgSeqs)
	if err != nil {
		writeJSONError(c, "invalid last_msg_seqs")
		s.observeConversationSync(ConversationSyncObservation{Result: "parse_last_msg_seqs_error", Duration: time.Since(start), OnlyUnread: onlyUnread, WithRecents: withRecents})
		return
	}
	result, err := s.conversations.Sync(c.Request.Context(), conversationusecase.SyncQuery{
		UID:                 req.UID,
		Version:             req.Version,
		LastMsgSeqs:         lastMsgSeqs,
		MsgCount:            req.MsgCount,
		OnlyUnread:          onlyUnread,
		ExcludeChannelTypes: append([]uint8(nil), req.ExcludeChannelTypes...),
		Limit:               req.Limit,
	})
	if err != nil {
		writeJSONError(c, err.Error())
		s.observeConversationSync(ConversationSyncObservation{Result: "error", Duration: time.Since(start), OnlyUnread: onlyUnread, WithRecents: withRecents})
		return
	}
	resp := make([]legacyConversationResponse, 0, len(result.Conversations))
	for _, item := range result.Conversations {
		resp = append(resp, newLegacyConversationResponse(req.UID, item))
	}
	s.observeConversationSync(ConversationSyncObservation{
		Result:             "ok",
		Duration:           time.Since(start),
		OnlyUnread:         onlyUnread,
		WithRecents:        withRecents,
		ReturnedItems:      len(result.Conversations),
		OverlayItems:       result.OverlayItems,
		RecentLoadDuration: result.RecentLoadDuration,
	})
	c.JSON(http.StatusOK, resp)
}

func (s *Server) observeConversationSync(event ConversationSyncObservation) {
	if s == nil || s.conversationSyncObserver == nil {
		return
	}
	if event.Result == "" {
		event.Result = "error"
	}
	if event.Duration <= 0 {
		event.Duration = time.Nanosecond
	}
	s.conversationSyncObserver.ObserveConversationSync(event)
}

func parseLegacyLastMsgSeqs(uid, raw string) (map[conversationusecase.ConversationKey]uint64, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	items := strings.Split(raw, "|")
	out := make(map[conversationusecase.ConversationKey]uint64, len(items))
	for _, item := range items {
		parts := strings.Split(item, ":")
		if len(parts) != 3 || parts[0] == "" {
			return nil, strconv.ErrSyntax
		}
		channelType, err := strconv.ParseUint(parts[1], 10, 8)
		if err != nil {
			return nil, err
		}
		lastMsgSeq, err := strconv.ParseUint(parts[2], 10, 64)
		if err != nil {
			return nil, err
		}
		channelID := parts[0]
		if uint8(channelType) == frame.ChannelTypePerson {
			channelID, err = runtimechannelid.NormalizePersonChannel(uid, channelID)
			if err != nil {
				return nil, err
			}
		}
		out[conversationusecase.ConversationKey{
			ChannelID:   channelID,
			ChannelType: int64(channelType),
		}] = lastMsgSeq
	}
	return out, nil
}

func newLegacyConversationResponse(uid string, item conversationusecase.SyncConversation) legacyConversationResponse {
	resp := legacyConversationResponse{
		ChannelID:       legacyMessageChannelID(uid, item.ChannelID, item.ChannelType),
		ChannelType:     item.ChannelType,
		Unread:          item.Unread,
		Timestamp:       item.Timestamp,
		LastMsgSeq:      item.LastMsgSeq,
		LastClientMsgNo: item.LastClientMsgNo,
		OffsetMsgSeq:    0,
		ReadedToMsgSeq:  item.ReadToMsgSeq,
		Version:         item.Version,
	}
	if len(item.Recents) > 0 {
		resp.Recents = make([]legacyMessageResp, 0, len(item.Recents))
		for _, msg := range item.Recents {
			resp.Recents = append(resp.Recents, newLegacyConversationRecentResp(uid, msg))
		}
	}
	return resp
}

func newLegacyConversationRecentResp(uid string, msg conversationusecase.SyncMessage) legacyMessageResp {
	return newLegacyMessageResp(uid, messageusecase.SyncedMessage{
		MessageID:   msg.MessageID,
		ClientMsgNo: msg.ClientMsgNo,
		MessageSeq:  msg.MessageSeq,
		FromUID:     msg.FromUID,
		ChannelID:   msg.ChannelID,
		ChannelType: msg.ChannelType,
		Timestamp:   int32(msg.ServerTimestampMS / 1000),
		Payload:     append([]byte(nil), msg.Payload...),
	})
}
