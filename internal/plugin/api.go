package plugin

import (
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type api struct {
	s *Server
	wklog.Log
}

func newApi(s *Server) *api {
	return &api{
		s:   s,
		Log: wklog.NewWKLog("plugin.api"),
	}
}

func (a *api) routes() {
	// ------------------- 插件 -------------------
	a.s.rpcServer.Route("/plugin/start", a.pluginStart) // 插件开始
	a.s.rpcServer.Route("/plugin/stop", a.pluginStop)   // 插件停止

	// ------------------- 消息 -------------------
	a.s.rpcServer.Route("/channel/messages", a.channelMessages) // 获取频道消息
}
