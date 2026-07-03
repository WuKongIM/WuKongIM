package api

import (
	"errors"
	"net/http"

	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/user"
	"github.com/gin-gonic/gin"
)

type deviceQuitRequest struct {
	UID        string `json:"uid"`
	DeviceFlag int    `json:"device_flag"`
}

type systemUIDsRequest struct {
	UIDs []string `json:"uids"`
}

func (s *Server) handleDeviceQuit(c *gin.Context) {
	var req deviceQuitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return
	}
	if err := s.requireUserUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.users.DeviceQuit(c.Request.Context(), user.DeviceQuitCommand{
		UID:        req.UID,
		DeviceFlag: req.DeviceFlag,
	}))
}

func (s *Server) handleOnlineStatus(c *gin.Context) {
	var uids []string
	if err := c.ShouldBindJSON(&uids); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return
	}
	if len(uids) == 0 {
		c.JSON(http.StatusOK, gin.H{"status": http.StatusOK})
		return
	}
	if err := s.requireUserUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	statuses, err := s.users.OnlineStatus(c.Request.Context(), uids)
	if err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, statuses)
}

func (s *Server) handleSystemUIDsAdd(c *gin.Context) {
	var req systemUIDsRequest
	if !bindSystemUIDsRequest(c, &req) {
		return
	}
	if err := s.requireUserUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.users.AddSystemUIDs(c.Request.Context(), req.UIDs))
}

func (s *Server) handleSystemUIDsRemove(c *gin.Context) {
	var req systemUIDsRequest
	if !bindSystemUIDsRequest(c, &req) {
		return
	}
	if err := s.requireUserUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.users.RemoveSystemUIDs(c.Request.Context(), req.UIDs))
}

func (s *Server) handleSystemUIDsGet(c *gin.Context) {
	if err := s.requireUserUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	uids, err := s.users.ListSystemUIDs(c.Request.Context())
	if err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, uids)
}

func (s *Server) handleSystemUIDsAddToCache(c *gin.Context) {
	var req systemUIDsRequest
	if !bindSystemUIDsRequest(c, &req) {
		return
	}
	if err := s.requireUserUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.users.AddSystemUIDsToCache(req.UIDs))
}

func (s *Server) handleSystemUIDsRemoveFromCache(c *gin.Context) {
	var req systemUIDsRequest
	if !bindSystemUIDsRequest(c, &req) {
		return
	}
	if err := s.requireUserUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.users.RemoveSystemUIDsFromCache(req.UIDs))
}

func bindSystemUIDsRequest(c *gin.Context, req *systemUIDsRequest) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return false
	}
	return true
}

func (s *Server) requireUserUsecase() error {
	if s == nil || s.users == nil {
		return errors.New("user usecase not configured")
	}
	return nil
}
