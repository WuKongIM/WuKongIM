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
	a.s.rpcServer.Route("/plugin/start", a.pluginStart)             // 插件开始
	a.s.rpcServer.Route("/plugin/stop", a.pluginStop)               // 插件停止
	a.s.rpcServer.Route("/plugin/httpForward", a.pluginHttpForward) // 插件HTTP转发

	// ------------------- 消息 -------------------
	a.s.rpcServer.Route("/channel/messages", a.channelMessages) // 获取频道消息

	// ------------------- 分布式 -------------------
	a.s.rpcServer.Route("/cluster/config", a.clusterConfig)                         // 获取分布式配置
	a.s.rpcServer.Route("/cluster/channels/belongNode", a.clusterChannelBelongNode) // 获取频道所属节点

	// ------------------- 最近会话 -------------------
	a.s.rpcServer.Route("/conversations/channels", a.conversationChannels) // 获取最近会话的频道集合
}
