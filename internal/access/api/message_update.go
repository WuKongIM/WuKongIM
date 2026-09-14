package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

type messageUpdateUsecase interface {
	UpdateMessage(context.Context, messageusecase.UpdateMessageCommand) (messageusecase.UpdateMessageResult, error)
	MessageUpdates(context.Context, messageusecase.MessageUpdatesQuery) (messageusecase.MessageUpdatesResult, error)
}
type updateMessageRequest struct {
	LoginUID             string  `json:"login_uid"`
	ChannelID            string  `json:"channel_id"`
	ChannelType          uint8   `json:"channel_type"`
	MessageID            string  `json:"message_id"`
	ExpectedContentEpoch *string `json:"expected_content_epoch"`
	ExpectedVersion      *string `json:"expected_version"`
	RequestID            string  `json:"request_id"`
	Payload              []byte  `json:"payload"`
}

type updateMessageResponse struct {
	MessageID   uint64 `json:"message_id,string"`
	MessageSeq  uint64 `json:"message_seq,string"`
	Version     uint64 `json:"version,string"`
	UpdatedAtMS int64  `json:"updated_at_ms"`
}

// messageUpdateResponse uses decimal strings for every 64-bit identity and cursor.
type messageUpdateResponse struct {
	legacyMessageResp
	MessageID  string `json:"message_id"`
	MessageSeq string `json:"message_seq"`
	Version    string `json:"version"`
}
type messageUpdatesRequest struct {
	LoginUID     string `json:"login_uid"`
	ChannelID    string `json:"channel_id"`
	ChannelType  uint8  `json:"channel_type"`
	UpdateCursor string `json:"update_cursor"`
	Limit        int    `json:"limit"`
}

func (s *Server) handleMessageUpdate(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2*metadb.MaxMessageUpdatePayload)
	var req updateMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ExpectedVersion == nil || req.ExpectedContentEpoch == nil {
		writeUpdateError(c, messageusecase.ErrUpdateInvalid)
		return
	}
	id, err := strconv.ParseUint(req.MessageID, 10, 64)
	if err != nil {
		writeUpdateError(c, messageusecase.ErrUpdateInvalid)
		return
	}
	version, err := strconv.ParseUint(*req.ExpectedVersion, 10, 64)
	if err != nil {
		writeUpdateError(c, messageusecase.ErrUpdateInvalid)
		return
	}
	epoch, err := strconv.ParseUint(*req.ExpectedContentEpoch, 10, 64)
	if err != nil {
		writeUpdateError(c, messageusecase.ErrUpdateInvalid)
		return
	}
	app, ok := s.messages.(messageUpdateUsecase)
	if !ok {
		writeUpdateError(c, messageusecase.ErrUpdateUnavailable)
		return
	}
	result, err := app.UpdateMessage(c.Request.Context(), messageusecase.UpdateMessageCommand{LoginUID: req.LoginUID, ChannelID: req.ChannelID, ChannelType: req.ChannelType, MessageID: id, ExpectedVersion: version, ExpectedContentEpoch: epoch, RequestID: req.RequestID, Payload: req.Payload})
	if err != nil {
		writeUpdateError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": http.StatusOK, "data": updateMessageResponse{MessageID: result.MessageID, MessageSeq: result.MessageSeq, Version: result.Version, UpdatedAtMS: result.UpdatedAtMS}})
}

func (s *Server) handleChannelMessageUpdates(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
	var req messageUpdatesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeUpdateError(c, messageusecase.ErrUpdateInvalid)
		return
	}
	app, ok := s.messages.(messageUpdateUsecase)
	if !ok {
		writeUpdateError(c, messageusecase.ErrUpdateUnavailable)
		return
	}
	result, err := app.MessageUpdates(c.Request.Context(), messageusecase.MessageUpdatesQuery{LoginUID: req.LoginUID, ChannelID: req.ChannelID, ChannelType: req.ChannelType, UpdateCursor: req.UpdateCursor, Limit: req.Limit})
	if err != nil {
		writeUpdateError(c, err)
		return
	}
	messages := make([]messageUpdateResponse, 0, len(result.Updates))
	for _, msg := range result.Updates {
		messages = append(messages, messageUpdateResponse{legacyMessageResp: newLegacyMessageResp(strings.TrimSpace(req.LoginUID), msg), MessageID: strconv.FormatUint(msg.MessageID, 10), MessageSeq: strconv.FormatUint(msg.MessageSeq, 10), Version: strconv.FormatUint(msg.Version, 10)})
	}
	c.JSON(http.StatusOK, gin.H{"updates": messages, "next_update_cursor": result.NextUpdateCursor, "more": result.More, "reset_required": result.ResetRequired})
}

func writeUpdateError(c *gin.Context, err error) {
	status := http.StatusServiceUnavailable
	code := "unavailable"
	var business messageusecase.UpdateError
	if errors.As(err, &business) {
		code = string(business)
		switch code {
		case "invalid_request":
			status = http.StatusBadRequest
		case "message_not_found":
			status = http.StatusNotFound
		case "message_not_updatable":
			status = http.StatusUnprocessableEntity
		case "content_epoch_conflict", "version_conflict", "idempotency_conflict", "reset_required", "stale_meta":
			status = http.StatusConflict
		case "resource_exhausted":
			status = http.StatusTooManyRequests
		}
	}
	if errors.Is(err, metadb.ErrInvalidArgument) {
		status = http.StatusBadRequest
		code = "invalid_request"
	}
	switch {
	case errors.Is(err, metadb.ErrNotFound), errors.Is(err, messageusecase.ErrChannelNotFound):
		status = http.StatusNotFound
		code = "message_not_found"
	case errors.Is(err, messageusecase.ErrSyncMembershipRequired), errors.Is(err, messageusecase.ErrSyncChannelDisbanded):
		status = http.StatusForbidden
		code = "channel_not_accessible"
	case errors.Is(err, messageusecase.ErrSyncLoginUIDRequired), errors.Is(err, messageusecase.ErrSyncChannelIDRequired), errors.Is(err, messageusecase.ErrSyncChannelTypeRequired):
		status = http.StatusBadRequest
		code = "invalid_request"
	}
	c.JSON(status, gin.H{"status": status, "code": code})
}
