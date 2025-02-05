package plugin

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/types/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/wkrpc"
	"go.uber.org/zap"
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
	a.s.rpcServer.Route("/plugin/start", a.pluginStart)
}

// 插件启动
func (a *api) pluginStart(c *wkrpc.Context) {
	pluginInfo := &pluginproto.PluginInfo{}
	err := pluginInfo.Unmarshal(c.Body())
	if err != nil {
		a.Error("PluginInfo unmarshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	a.s.pluginManager.add(newPlugin(a.s, c.Conn(), pluginInfo))

	fmt.Println("plugin start", pluginInfo)

	c.WriteOk()
}

// 插件停止
func (a *api) pluginStop(c *wkrpc.Context) {
	pluginInfo := &pluginproto.PluginInfo{}
	err := pluginInfo.Unmarshal(c.Body())
	if err != nil {
		a.Error("PluginInfo unmarshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	a.s.pluginManager.remove(pluginInfo.No)
	c.WriteOk()
}
