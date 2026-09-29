package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

type userSendBanUsecase interface {
	SetSendBan(context.Context, string, int64, *uint64) (metadb.SendBanResult, error)
	GetSendBan(context.Context, string) (metadb.SendBanResult, error)
}
type channelSendBanUsecase interface {
	SetSendBan(context.Context, channelusecase.ChannelKey, int64, *uint64) (metadb.SendBanResult, error)
	GetSendBan(context.Context, channelusecase.ChannelKey) (metadb.SendBanResult, error)
}
type sendBanRequest struct {
	UID             string  `json:"uid"`
	ChannelID       string  `json:"channel_id"`
	ChannelType     uint8   `json:"channel_type"`
	SendBan         *int64  `json:"send_ban"`
	ExpectedVersion *string `json:"expected_version"`
}

func (s *Server) registerSendBanRoutes() {
	s.engine.POST("/user/send_ban", func(c *gin.Context) { s.handleSendBan(c, true, true) })
	s.engine.GET("/user/send_ban", func(c *gin.Context) { s.handleSendBan(c, true, false) })
	s.engine.POST("/channel/send_ban", func(c *gin.Context) { s.handleSendBan(c, false, true) })
	s.engine.GET("/channel/send_ban", func(c *gin.Context) { s.handleSendBan(c, false, false) })
}
func (s *Server) handleSendBan(c *gin.Context, user, write bool) {
	var req sendBanRequest
	invalid := func() {
		c.JSON(http.StatusBadRequest, gin.H{"status": 400, "code": "invalid_request", "msg": "invalid send ban request"})
	}
	if write {
		d := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
		d.DisallowUnknownFields()
		if d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF || req.SendBan == nil {
			invalid()
			return
		}
	} else {
		req.UID = c.Query("uid")
		req.ChannelID = c.Query("channel_id")
		if !user {
			n, err := strconv.ParseUint(c.Query("channel_type"), 10, 8)
			if err != nil {
				invalid()
				return
			}
			req.ChannelType = uint8(n)
		}
	}
	if (user && (req.UID == "" || req.ChannelID != "" || req.ChannelType != 0)) || (!user && req.UID != "") {
		invalid()
		return
	}
	var expected *uint64
	if req.ExpectedVersion != nil {
		v, err := strconv.ParseUint(*req.ExpectedVersion, 10, 64)
		if err != nil || strconv.FormatUint(v, 10) != *req.ExpectedVersion {
			invalid()
			return
		}
		expected = &v
	}
	var out metadb.SendBanResult
	var err error
	if user {
		usecase, ok := s.users.(userSendBanUsecase)
		if !ok {
			writeSendBanResult(c, req, out, errors.New("unavailable"))
			return
		}
		if write {
			out, err = usecase.SetSendBan(c.Request.Context(), req.UID, *req.SendBan, expected)
		} else {
			out, err = usecase.GetSendBan(c.Request.Context(), req.UID)
		}
	} else {
		usecase, ok := s.channels.(channelSendBanUsecase)
		if !ok {
			writeSendBanResult(c, req, out, errors.New("unavailable"))
			return
		}
		key := channelusecase.ChannelKey{ChannelID: req.ChannelID, ChannelType: req.ChannelType}
		if write {
			out, err = usecase.SetSendBan(c.Request.Context(), key, *req.SendBan, expected)
		} else {
			out, err = usecase.GetSendBan(c.Request.Context(), key)
		}
	}
	writeSendBanResult(c, req, out, err)
}
func writeSendBanResult(c *gin.Context, req sendBanRequest, out metadb.SendBanResult, err error) {
	status, code := http.StatusOK, out.Status
	if err != nil {
		status = 503
		code = "temporarily_unavailable"
		if errors.Is(err, metadb.ErrInvalidArgument) {
			status = 400
			code = "invalid_request"
		}
	} else {
		switch code {
		case "ok":
		case "not_found":
			status = 404
			code = "channel_not_found"
		case "version_conflict", "channel_disbanded", "version_exhausted":
			status = 409
		default:
			status = 503
			code = "temporarily_unavailable"
		}
	}
	if status != 200 {
		c.JSON(status, gin.H{"status": status, "code": code, "msg": code})
		return
	}
	data := gin.H{"send_ban": out.SendBan, "send_ban_version": strconv.FormatUint(out.Version, 10)}
	if req.UID != "" {
		data["uid"] = req.UID
	} else {
		data["channel_id"] = req.ChannelID
		data["channel_type"] = req.ChannelType
	}
	c.JSON(status, gin.H{"status": status, "data": data})
}
