package api

import (
	"errors"
	"net/http"

	userusecase "github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/gin-gonic/gin"
)

type deviceQuitRequest struct {
	UID        string `json:"uid"`
	DeviceFlag int    `json:"device_flag"`
}

type systemUIDsRequest struct {
	UIDs []string `json:"uids"`
}

func (s *Server) registerUserRoutes() {
	if s == nil || s.engine == nil {
		return
	}
	s.engine.POST("/user/token", s.handleUpdateToken)
	s.engine.POST("/user/device_quit", s.handleDeviceQuit)
	s.engine.POST("/user/onlinestatus", s.handleOnlineStatus)
	s.engine.POST("/user/systemuids_add", s.handleSystemUIDsAdd)
	s.engine.POST("/user/systemuids_remove", s.handleSystemUIDsRemove)
	s.engine.GET("/user/systemuids", s.handleSystemUIDsGet)
	s.engine.POST("/user/systemuids_add_to_cache", s.handleSystemUIDsAddToCache)
	s.engine.POST("/user/systemuids_remove_from_cache", s.handleSystemUIDsRemoveFromCache)
}

func (s *Server) handleDeviceQuit(c *gin.Context) {
	var req deviceQuitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeJSONError(c, "数据格式有误！")
		return
	}
	if err := s.requireUserUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.users.DeviceQuit(c.Request.Context(), userusecase.DeviceQuitCommand{
		UID:        req.UID,
		DeviceFlag: req.DeviceFlag,
	}))
}

func (s *Server) handleOnlineStatus(c *gin.Context) {
	var uids []string
	if err := c.ShouldBindJSON(&uids); err != nil {
		writeJSONError(c, "数据格式有误！")
		return
	}
	if len(uids) == 0 {
		c.JSON(http.StatusOK, gin.H{"status": http.StatusOK})
		return
	}
	if err := s.requireUserUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	statuses, err := s.users.OnlineStatus(c.Request.Context(), uids)
	if err != nil {
		writeJSONError(c, err.Error())
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
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.users.AddSystemUIDs(c.Request.Context(), req.UIDs))
}

func (s *Server) handleSystemUIDsRemove(c *gin.Context) {
	var req systemUIDsRequest
	if !bindSystemUIDsRequest(c, &req) {
		return
	}
	if err := s.requireUserUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.users.RemoveSystemUIDs(c.Request.Context(), req.UIDs))
}

func (s *Server) handleSystemUIDsGet(c *gin.Context) {
	if err := s.requireUserUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	uids, err := s.users.ListSystemUIDs(c.Request.Context())
	if err != nil {
		writeJSONError(c, err.Error())
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
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.users.AddSystemUIDsToCache(req.UIDs))
}

func (s *Server) handleSystemUIDsRemoveFromCache(c *gin.Context) {
	var req systemUIDsRequest
	if !bindSystemUIDsRequest(c, &req) {
		return
	}
	if err := s.requireUserUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.users.RemoveSystemUIDsFromCache(req.UIDs))
}

func bindSystemUIDsRequest(c *gin.Context, req *systemUIDsRequest) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		writeJSONError(c, "数据格式有误！")
		return false
	}
	return true
}

func (s *Server) requireUserUsecase() error {
	if s == nil || s.users == nil {
		return errUserUsecaseRequired
	}
	return nil
}

var errUserUsecaseRequired = errors.New("user usecase not configured")
