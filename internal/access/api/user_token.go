package api

import (
	"net/http"

	"github.com/WuKongIM/WuKongIM/internal/contracts/protocolmeta"
	userusecase "github.com/WuKongIM/WuKongIM/internal/usecase/user"
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
	if err := s.requireUserUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	err := s.users.UpdateToken(c.Request.Context(), userusecase.UpdateTokenCommand{
		UID:         req.UID,
		Token:       req.Token,
		DeviceFlag:  protocolmeta.DeviceFlag(req.DeviceFlag),
		DeviceLevel: protocolmeta.DeviceLevel(req.DeviceLevel),
	})
	if err != nil {
		writeJSONError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": http.StatusOK})
}
