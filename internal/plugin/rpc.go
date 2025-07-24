package plugin

import (
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type rpc struct {
	s *Server
	wklog.Log
}

func newRpc(s *Server) *rpc {
	return &rpc{
		s:   s,
		Log: wklog.NewWKLog("plugin.rpc"),
	}
}

func (a *rpc) routes() {
	// ------------------- 插件 -------------------
	a.s.rpcServer.Route("/plugin/start", a.pluginStart) // 插件开始
	// a.s.rpcServer.Route("/plugin/stop", a.pluginStop)               // 插件停止
	a.s.rpcServer.Route("/close", a.pluginClose)                    // 插件关闭(当插件离线的时候会调用)
	a.s.rpcServer.Route("/plugin/httpForward", a.pluginHttpForward) // 插件HTTP转发

	// ------------------- 消息 -------------------
	a.s.rpcServer.Route("/channel/messages", a.channelMessages) // 获取频道消息
	a.s.rpcServer.Route("/message/send", a.messageSend)         // 发送消息

	// ------------------- 分布式 -------------------
	a.s.rpcServer.Route("/cluster/config", a.clusterConfig)                         // 获取分布式配置
	a.s.rpcServer.Route("/cluster/channels/belongNode", a.clusterChannelBelongNode) // 获取频道所属节点

	// ------------------- 最近会话 -------------------
	a.s.rpcServer.Route("/conversation/channels", a.conversationChannels) // 获取最近会话的频道集合

	// ------------------- stream -------------------
	a.s.rpcServer.Route("/stream/open", a.streamOpen)   // 流消息打开
	a.s.rpcServer.Route("/stream/close", a.streamClose) // 流消息关闭
	a.s.rpcServer.Route("/stream/write", a.streamWrite) // 流消息写入

}
