package cluster

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"go.uber.org/zap"
)

func (s *Server) ServerAPI(route *wkhttp.WKHttp, prefix string) {

	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	prefix = strings.TrimSuffix(prefix, "/")

	route.GET(fmt.Sprintf("%s/channel/clusterinfo", prefix), s.getAllClusterInfo) // 获取所有channel的集群信息
}

// 获取所有channel的集群信息
func (s *Server) getAllClusterInfo(c *wkhttp.Context) {
	offsetStr := c.Query("offset")
	limitStr := c.Query("limit")
	var offset, limit int64 = 0, 100
	if offsetStr != "" {
		offset, _ = strconv.ParseInt(offsetStr, 10, 64)
	}
	if limitStr != "" {
		limit, _ = strconv.ParseInt(limitStr, 10, 64)
	}

	channelClusterInfos, err := s.stateMachine.getChannelClusterInfos(int(offset), int(limit))
	if err != nil {
		s.Error("getChannelClusterInfos error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if channelClusterInfos == nil {
		channelClusterInfos = make([]*ChannelClusterInfo, 0)
	}
	c.JSON(http.StatusOK, channelClusterInfos)

}
