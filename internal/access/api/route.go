package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type legacyUserAddrResponse struct {
	TCPAddr string   `json:"tcp_addr"`
	WSAddr  string   `json:"ws_addr"`
	WSSAddr string   `json:"wss_addr"`
	UIDs    []string `json:"uids,omitempty"`
}

func (s *Server) handleRoute(c *gin.Context) {
	if c == nil {
		return
	}
	addr := s.legacyRouteAddresses(c)
	c.JSON(http.StatusOK, gin.H{
		"tcp_addr": addr.TCPAddr,
		"ws_addr":  addr.WSAddr,
		"wss_addr": addr.WSSAddr,
	})
}

func (s *Server) handleRouteBatch(c *gin.Context) {
	if c == nil {
		return
	}

	var uids []string
	if err := c.ShouldBindJSON(&uids); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"msg":    "数据格式有误！",
			"status": http.StatusBadRequest,
		})
		return
	}

	addr := s.legacyRouteAddresses(c)
	c.JSON(http.StatusOK, []legacyUserAddrResponse{
		{
			UIDs:    uids,
			TCPAddr: addr.TCPAddr,
			WSAddr:  addr.WSAddr,
			WSSAddr: addr.WSSAddr,
		},
	})
}

func (s *Server) legacyRouteAddresses(c *gin.Context) LegacyRouteAddresses {
	if s == nil {
		return LegacyRouteAddresses{}
	}
	if legacyRouteUseIntranet(c) {
		return s.legacyRouteIntranet
	}
	return s.legacyRouteExternal
}

func legacyRouteUseIntranet(c *gin.Context) bool {
	if c == nil {
		return false
	}
	v, err := strconv.Atoi(c.Query("intranet"))
	return err == nil && v != 0
}
