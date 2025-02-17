package plugin

import (
	"context"
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

	a.s.pluginManager.add(newPlugin(a.s, c.Conn(), pluginInfo))

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

	startupResp := &pluginproto.StartupResp{
		NodeId:     options.G.Cluster.NodeId,
		Success:    true,
		SandboxDir: sandboxDir,
	}

	data, err := startupResp.Marshal()
	if err != nil {
		a.Error("StartupResp marshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	c.Write(data)
}

// 插件停止
func (a *rpc) pluginStop(c *wkrpc.Context) {
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
