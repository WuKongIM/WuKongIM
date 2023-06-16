package server

import (
	"net/http"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type MonitorAPI struct {
	wklog.Log
	s *Server
}

// NewMonitorAPI NewMonitorAPI
func NewMonitorAPI(s *Server) *MonitorAPI {
	return &MonitorAPI{
		Log: wklog.NewWKLog("MonitorAPI"),
		s:   s,
	}
}

// Route 用户相关路由配置
func (m *MonitorAPI) Route(r *wkhttp.WKHttp) {
	r.GET("/metrics", m.s.monitor.Monitor)

	r.GET("/chart/realtime", m.realtime)
	// r.GET("/chart/upstream_packet_count", m.upstreamPacketCount)

}

func (m *MonitorAPI) realtime(c *wkhttp.Context) {
	last := c.Query("last")
	connNums := m.s.monitor.ConnNums()
	upstreamPackages := m.s.monitor.UpstreamPackageSample()
	upstreamTraffics := m.s.monitor.UpstreamTrafficSample()

	downstreamPackages := m.s.monitor.DownstreamPackageSample()
	downstreamTraffics := m.s.monitor.DownstreamTrafficSample()

	var realtimeData realtimeData
	if last == "1" {
		if len(connNums) > 0 {
			realtimeData.ConnNums = connNums[len(connNums)-1:]
		}
		if len(upstreamPackages) > 0 {
			realtimeData.UpstreamPackets = upstreamPackages[len(upstreamPackages)-1:]
		}
		if len(downstreamPackages) > 0 {
			realtimeData.DownstreamPackets = downstreamPackages[len(downstreamPackages)-1:]
		}
		if len(upstreamTraffics) > 0 {
			realtimeData.UpstreamTraffics = upstreamTraffics[len(upstreamTraffics)-1:]
		}
		if len(downstreamTraffics) > 0 {
			realtimeData.DownstreamTraffics = downstreamTraffics[len(downstreamTraffics)-1:]
		}
	} else {
		realtimeData.ConnNums = connNums
		realtimeData.UpstreamPackets = upstreamPackages
		realtimeData.DownstreamPackets = downstreamPackages
		realtimeData.UpstreamTraffics = upstreamTraffics
		realtimeData.DownstreamTraffics = downstreamTraffics
	}
	c.JSON(http.StatusOK, realtimeData)
}

type realtimeData struct {
	ConnNums           []int `json:"conn_nums"`
	UpstreamPackets    []int `json:"upstream_packets"`
	UpstreamTraffics   []int `json:"upstream_traffics"`
	DownstreamPackets  []int `json:"downstream_packets"`
	DownstreamTraffics []int `json:"downstream_traffics"`
}
