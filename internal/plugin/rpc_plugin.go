package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/internal/types/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/WuKongIM/wkrpc"
	"github.com/sendgrid/rest"
	"go.uber.org/zap"
)

const (
	AllNode int64 = -1
)

// 插件启动
func (a *rpc) pluginStart(c *wkrpc.Context) {
	pluginInfo := &pluginproto.PluginInfo{}
	err := pluginInfo.Unmarshal(c.Body())
	if err != nil {
		a.Error("PluginInfo unmarshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	if strings.TrimSpace(pluginInfo.No) == "" {
		a.Error("plugin start failed, plugin no is empty")
		c.WriteErr(fmt.Errorf("plugin no is empty"))
		return
	}

	plugin := newPlugin(a.s, c.Conn(), pluginInfo)
	a.s.pluginManager.add(plugin)

	a.Info("plugin start", zap.Any("pluginInfo", pluginInfo))

	sandboxDir := path.Join(a.s.sandboxDir, pluginInfo.No)

	// 沙盒如果是相对路径则转换为绝对路径
	if !path.IsAbs(sandboxDir) {
		sandboxDir, err = filepath.Abs(sandboxDir)
		if err != nil {
			a.Error("plugin start failed, get abs path failed", zap.Error(err))
			c.WriteErr(err)
			return
		}
	}

	// 如果沙盒目录不存在则创建
	if _, err := os.Stat(sandboxDir); os.IsNotExist(err) {
		err := os.MkdirAll(sandboxDir, os.ModePerm)
		if err != nil {
			panic(err)
		}
	}

	existPlugin, err := service.Store.DB().GetPlugin(pluginInfo.No)
	if err != nil {
		a.Error("get plugin failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	var createdAt *time.Time
	var updatedAt = wkutil.TimePtr(time.Now())
	if wkdb.IsEmptyPlugin(existPlugin) {
		createdAt = wkutil.TimePtr(time.Now())
	}

	var configTemplateBytes []byte
	if pluginInfo.ConfigTemplate != nil {
		configTemplateBytes, err = pluginInfo.ConfigTemplate.Marshal()
		if err != nil {
			a.Error("plugin config marshal failed", zap.Error(err))
			c.WriteErr(err)
			return
		}
	}

	newPlugin := wkdb.Plugin{
		No:             pluginInfo.No,
		Name:           pluginInfo.Name,
		Version:        pluginInfo.Version,
		Priority:       uint32(pluginInfo.Priority),
		Methods:        pluginInfo.Methods,
		Status:         existPlugin.Status,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		ConfigTemplate: configTemplateBytes,
		Config:         existPlugin.Config,
	}

	// 如果插件信息有变更则更新
	if a.pluginChange(existPlugin, newPlugin) {
		err = service.Store.DB().AddOrUpdatePlugin(newPlugin)
		if err != nil {
			a.Error("add or update plugin failed", zap.Error(err), zap.Any("plugin", newPlugin))
			c.WriteErr(err)
			return
		}
	}

	var configBytes []byte
	if len(newPlugin.Config) > 0 {
		err = plugin.UpdateConfig(newPlugin.Config)
		if err != nil {
			a.Error("plugin update config failed", zap.Error(err))
			c.WriteErr(err)
			return
		}

		configBytes, err = json.Marshal(newPlugin.Config)
		if err != nil {
			a.Error("plugin config marshal failed", zap.Error(err))
			c.WriteErr(err)
			return
		}
	}

	startupResp := &pluginproto.StartupResp{
		NodeId:     options.G.Cluster.NodeId,
		Success:    true,
		SandboxDir: sandboxDir,
		Config:     configBytes,
	}

	data, err := startupResp.Marshal()
	if err != nil {
		a.Error("StartupResp marshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	c.Write(data)
}

// 插件是否变更
func (a *rpc) pluginChange(plugin1, plugin2 wkdb.Plugin) bool {
	if plugin1.No != plugin2.No || plugin1.Version != plugin2.Version {
		return true
	}
	if plugin1.Name != plugin2.Name || !bytes.Equal(plugin1.ConfigTemplate, plugin2.ConfigTemplate) {
		return true
	}
	if plugin1.Priority != plugin2.Priority || !wkutil.ArrayEqual(plugin1.Methods, plugin2.Methods) {
		return true
	}
	return false
}

// // 插件停止
// func (a *rpc) pluginStop(c *wkrpc.Context) {
// 	pluginInfo := &pluginproto.PluginInfo{}
// 	err := pluginInfo.Unmarshal(c.Body())
// 	if err != nil {
// 		a.Error("PluginInfo unmarshal failed", zap.Error(err))
// 		c.WriteErr(err)
// 		return
// 	}
// 	a.s.pluginManager.remove(pluginInfo.No)

// 	a.Info("plugin stop", zap.Any("pluginInfo", pluginInfo))

// 	c.WriteOk()
// }

func (a *rpc) pluginClose(c *wkrpc.Context) {
	pluginNo := c.Uid()
	a.s.pluginManager.remove(pluginNo)
	a.Info("plugin close", zap.String("pluginNo", pluginNo))
	c.WriteOk()
}

func (a *rpc) pluginHttpForward(c *wkrpc.Context) {
	forwardReq := &pluginproto.ForwardHttpReq{}
	err := forwardReq.Unmarshal(c.Body())
	if err != nil {
		a.Error("PluginRouteReq unmarshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	// ---------- 如果指定了节点，且不是本地节点，则转发到指定节点 ----------
	if forwardReq.ToNodeId > 0 && !options.G.IsLocalNode(uint64(forwardReq.ToNodeId)) {
		node := service.Cluster.NodeInfoById(uint64(forwardReq.ToNodeId))
		if node == nil {
			a.Error("plugin http forward failed, node not found", zap.Int64("nodeId", forwardReq.ToNodeId))
			c.WriteErr(fmt.Errorf("node not found"))
			return
		}
		pluginUrl := fmt.Sprintf("%s/plugins/%s%s", node.ApiServerAddr, forwardReq.PluginNo, forwardReq.Request.Path)
		resp, err := a.ForwardWithBody(pluginUrl, forwardReq.Request)
		if err != nil {
			a.Error("plugin http forward failed", zap.Error(err))
			c.WriteErr(err)
			return
		}
		data, err := resp.Marshal()
		if err != nil {
			a.Error("PluginRouteResp marshal failed", zap.Error(err))
			c.WriteErr(err)
			return
		}
		c.Write(data)
		return
	}

	// ---------- 处理本地节点的请求 ----------
	plugin := a.s.pluginManager.get(forwardReq.PluginNo)
	if plugin == nil {
		a.Error("plugin http forward failed, plugin not found", zap.String("pluginNo", forwardReq.PluginNo))
		c.WriteErr(fmt.Errorf("plugin not found"))
		return
	}
	if plugin.Status() != types.PluginStatusNormal {
		a.Error("plugin http forward failed, plugin not running", zap.String("pluginNo", forwardReq.PluginNo))
		c.WriteErr(fmt.Errorf("plugin not running"))
		return
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	resp, err := plugin.Route(timeoutCtx, forwardReq.Request)
	if err != nil {
		a.Error("plugin http forward failed, plugin route failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	data, err := resp.Marshal()
	if err != nil {
		a.Error("PluginRouteResp marshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(data)
}

func (a *rpc) ForwardWithBody(url string, req *pluginproto.HttpRequest) (*pluginproto.HttpResponse, error) {
	r := rest.Request{
		Method:      rest.Method(strings.ToUpper(req.Method)),
		BaseURL:     url,
		Headers:     req.Headers,
		Body:        req.Body,
		QueryParams: req.Query,
	}

	resp, err := rest.Send(r)
	if err != nil {
		return nil, err
	}

	respHeaders := make(map[string]string)
	for k, v := range resp.Headers {
		respHeaders[k] = v[0]
	}

	rsp := &pluginproto.HttpResponse{
		Status:  int32(resp.StatusCode),
		Headers: respHeaders,
		Body:    []byte(resp.Body),
	}
	return rsp, nil
}
