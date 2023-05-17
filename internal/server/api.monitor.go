package server

import (
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

}
