package plugin

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/types/pluginproto"
	"github.com/WuKongIM/wkrpc"
	"go.uber.org/zap"
)

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

	a.Info("plugin start", zap.Any("pluginInfo", pluginInfo))

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
