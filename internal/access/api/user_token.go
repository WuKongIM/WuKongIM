package api

import (
	"net/http"

	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/gin-gonic/gin"
)

type updateTokenRequest struct {
	UID         string `json:"uid"`
	Token       string `json:"token"`
	DeviceFlag  uint8  `json:"device_flag"`
	DeviceLevel uint8  `json:"device_level"`
}

func (s *Server) handleUpdateToken(c *gin.Context) {
	var req updateTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "invalid request", "status": http.StatusBadRequest})
		return
	}
	if s == nil || s.users == nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "user usecase not configured", "status": http.StatusBadRequest})
		return
	}
	err := s.users.UpdateToken(c.Request.Context(), user.UpdateTokenCommand{
		UID:         req.UID,
		Token:       req.Token,
		DeviceFlag:  frame.DeviceFlag(req.DeviceFlag),
		DeviceLevel: frame.DeviceLevel(req.DeviceLevel),
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": err.Error(), "status": http.StatusBadRequest})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": http.StatusOK})
}
