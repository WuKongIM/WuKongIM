package api

import (
	"encoding/base64"
	"net/http"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/gin-gonic/gin"
)

type sendMessageRequest struct {
	FromUID       string `json:"from_uid"`
	LegacyFromUID string `json:"sender_uid"`
	ChannelID     string `json:"channel_id"`
	ChannelType   uint8  `json:"channel_type"`
	Payload       string `json:"payload"`
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
	if req.FromUID == "" || req.ChannelID == "" || req.ChannelType == 0 || req.Payload == "" {
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

	channelID := req.ChannelID
	if req.ChannelType == frame.ChannelTypePerson {
		channelID, err = runtimechannelid.NormalizePersonChannel(req.FromUID, req.ChannelID)
		if err != nil {
			writeJSONError(c, http.StatusBadRequest, "invalid channel id")
			return
		}
	}

	result, err := s.messages.Send(c.Request.Context(), message.SendCommand{
		FromUID:         req.FromUID,
		ChannelID:       channelID,
		ChannelType:     req.ChannelType,
		Payload:         payload,
		ProtocolVersion: frame.LatestVersion,
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
