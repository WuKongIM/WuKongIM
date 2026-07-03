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
	addr, ok := s.legacyRouteAddresses(c)
	if !ok {
		writeLegacyRouteNodeError(c)
		return
	}
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

	addr, ok := s.legacyRouteAddresses(c)
	if !ok {
		writeLegacyRouteNodeError(c)
		return
	}
	c.JSON(http.StatusOK, []legacyUserAddrResponse{
		{
			UIDs:    uids,
			TCPAddr: addr.TCPAddr,
			WSAddr:  addr.WSAddr,
			WSSAddr: addr.WSSAddr,
		},
	})
}

func (s *Server) legacyRouteAddresses(c *gin.Context) (LegacyRouteAddresses, bool) {
	if s == nil {
		return LegacyRouteAddresses{}, true
	}
	nodeID, specified, ok := legacyRouteNodeID(c)
	if !ok {
		return LegacyRouteAddresses{}, false
	}
	if specified {
		node, exists := s.legacyRouteNodes[nodeID]
		if !exists {
			return LegacyRouteAddresses{}, false
		}
		if legacyRouteUseIntranet(c) {
			return node.Intranet, true
		}
		return node.External, true
	}
	if legacyRouteUseIntranet(c) {
		return s.legacyRouteIntranet, true
	}
	return s.legacyRouteExternal, true
}

func legacyRouteUseIntranet(c *gin.Context) bool {
	if c == nil {
		return false
	}
	v, err := strconv.Atoi(c.Query("intranet"))
	return err == nil && v != 0
}

func legacyRouteNodeID(c *gin.Context) (uint64, bool, bool) {
	if c == nil {
		return 0, false, true
	}
	raw, specified := legacyRouteNodeQuery(c)
	if !specified {
		return 0, false, true
	}
	nodeID, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || nodeID == 0 {
		return 0, true, false
	}
	return nodeID, true, true
}

func legacyRouteNodeQuery(c *gin.Context) (string, bool) {
	for _, key := range []string{"node_id", "nodeId", "nodeID"} {
		if value, ok := c.GetQuery(key); ok {
			return value, true
		}
	}
	return "", false
}

func writeLegacyRouteNodeError(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{
		"msg":    "节点参数有误！",
		"status": http.StatusBadRequest,
	})
}
