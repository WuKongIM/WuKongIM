package api

import (
	"encoding/base64"
	"net/http"

	"github.com/WuKongIM/WuKongIM/internal/legacy/observability/diagnostics/tracectx"
	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/gin-gonic/gin"
)

type sendMessageRequest struct {
	FromUID       string                   `json:"from_uid"`
	LegacyFromUID string                   `json:"sender_uid"`
	ChannelID     string                   `json:"channel_id"`
	ChannelType   uint8                    `json:"channel_type"`
	ClientMsgNo   string                   `json:"client_msg_no"`
	Payload       string                   `json:"payload"`
	Subscribers   []string                 `json:"subscribers"`
	Header        sendMessageHeaderRequest `json:"header"`
	NoPersist     int                      `json:"no_persist"`
	SyncOnce      int                      `json:"sync_once"`
}

type sendMessageHeaderRequest struct {
	// NoPersist marks the send as non-durable when non-zero.
	NoPersist int `json:"no_persist"`
	// SyncOnce marks the send as a one-shot command-channel message when non-zero.
	SyncOnce int `json:"sync_once"`
}

type sendMessageResponse struct {
	MessageID  int64  `json:"message_id"`
	MessageSeq uint64 `json:"message_seq"`
	Reason     uint8  `json:"reason"`
}

func (s *Server) handleSendMessage(c *gin.Context) {
	var req sendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeJSONError(c, http.StatusBadRequest, "invalid request")
		return
	}
	if req.FromUID == "" {
		req.FromUID = req.LegacyFromUID
	}
	requestScoped := len(req.Subscribers) > 0
	if req.FromUID == "" || req.Payload == "" {
		writeJSONError(c, http.StatusBadRequest, "invalid request")
		return
	}
	if requestScoped {
		if req.ChannelID != "" {
			writeJSONError(c, http.StatusBadRequest, "invalid request")
			return
		}
	} else if req.ChannelID == "" || req.ChannelType == 0 {
		writeJSONError(c, http.StatusBadRequest, "invalid request")
		return
	}

	payload, err := base64.StdEncoding.DecodeString(req.Payload)
	if err != nil {
		writeJSONError(c, http.StatusBadRequest, "invalid payload")
		return
	}

	if s == nil || s.messages == nil {
		writeJSONError(c, http.StatusInternalServerError, "message usecase not configured")
		return
	}

	reqCtx := c.Request.Context()
	if traceID, ok := tracectx.ValidateHeaderTraceID(c.GetHeader("X-WK-Trace-ID")); ok {
		reqCtx = tracectx.WithContext(reqCtx, tracectx.Context{TraceID: traceID, Sampled: true})
	}
	reqCtx, traceCtx := tracectx.Ensure(reqCtx, nil)
	noPersist := req.Header.NoPersist != 0 || req.NoPersist != 0
	syncOnce := req.Header.SyncOnce != 0 || req.SyncOnce != 0
	channelID := req.ChannelID
	channelType := req.ChannelType
	if requestScoped {
		channelID = ""
		channelType = 0
	}

	result, err := s.messages.Send(reqCtx, message.SendCommand{
		TraceID:            traceCtx.TraceID,
		Framer:             frame.Framer{NoPersist: noPersist, SyncOnce: syncOnce},
		FromUID:            req.FromUID,
		ChannelID:          channelID,
		ChannelType:        channelType,
		RequestSubscribers: req.Subscribers,
		ClientMsgNo:        req.ClientMsgNo,
		Payload:            payload,
		ProtocolVersion:    frame.LatestVersion,
	})
	if err != nil {
		if status, msg, ok := mapSendError(err); ok {
			writeJSONError(c, status, msg)
			return
		}
		writeJSONError(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, sendMessageResponse{
		MessageID:  result.MessageID,
		MessageSeq: result.MessageSeq,
		Reason:     uint8(result.Reason),
	})
}

func writeJSONError(c *gin.Context, status int, message string) {
	if c == nil {
		return
	}
	if message == "" {
		message = http.StatusText(status)
	}
	c.JSON(status, gin.H{"error": message})
}
