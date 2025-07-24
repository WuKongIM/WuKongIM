package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/internal/types/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/auth"
	"github.com/WuKongIM/WuKongIM/pkg/auth/resource"
	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *Server) SetRoute(r *wkhttp.WKHttp) {

	r.Any("/plugins/:plugin/*path", s.handlePluginRoute) // 处理插件的路由，将http请求转发给插件

	r.GET("/plugins", s.handleGetPlugins) // 获取插件列表

	r.POST("/pluginconfig/:plugin", s.handleUpdatePluginConfig) // 更新插件配置
	r.POST("/plugin/bind", s.handlePluginBind)                  // 绑定插件
	r.GET("/plugin/bind", s.handlePluginBindList)               // 获取插件绑定列表
	r.POST("/plugin/unbind", s.handlePluginUnbind)              // 解绑插件
	r.POST("/plugin/uninstall", s.handleUninstall)              // 卸载插件
}

// 获取插件列表

func (s *Server) handleGetPlugins(c *wkhttp.Context) {

	typeStr := c.Query("type") // 插件类型 ai: 机器人插件
	nodeId := wkutil.ParseUint64(c.Query("node_id"))

	if nodeId == 0 {
		nodeId = options.G.Cluster.NodeId
	}

	if !options.G.IsLocalNode(nodeId) {
		node := service.Cluster.NodeInfoById(nodeId)
		if node == nil {
			s.Error("node not found", zap.Uint64("node_id", nodeId))
			c.ResponseError(fmt.Errorf("node not found"))
			return
		}
		// 转发到指定节点
		c.Forward(fmt.Sprintf("%s%s", node.ApiServerAddr, c.Request.URL.Path))
		return
	}

	plugins, err := service.Store.DB().GetPlugins()
	if err != nil {
		s.Error("get plugins failed", zap.Error(err))
		c.ResponseError(err)
		return
	}

	resps := make([]*pluginResp, 0, len(plugins))
	for _, p := range plugins {
		// 如果是机器人插件，只返回有Receive方法的插件
		if typeStr != "" && strings.ToLower(typeStr) == "ai" {
			if len(p.Methods) == 0 || !wkutil.ArrayContains(p.Methods, types.PluginReceive.String()) {
				continue
			}
		}
		var status types.PluginStatus
		if p.Status == wkdb.PluginStatusDisabled {
			status = types.PluginStatusDisabled
		} else {
			pg := s.pluginManager.get(p.No)
			if pg != nil {
				status = pg.Status()
			} else {
				status = types.PluginStatusOffline
			}
		}

		resp := newPluginResp(p)
		resp.Status = status
		resp.NodeId = nodeId

		if typeStr != "" && strings.ToLower(typeStr) == "ai" && resp.IsAI == 0 {
			continue
		}
		resps = append(resps, resp)
	}
	c.JSON(http.StatusOK, resps)
}

// 处理插件的路由
func (s *Server) handlePluginRoute(c *wkhttp.Context) {
	pluginNo := c.Param("plugin")
	plugin := s.pluginManager.get(pluginNo)
	if plugin == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"msg":    "plugin not found",
			"status": http.StatusNotFound,
		})
		return
	}

	if plugin.Status() != types.PluginStatusNormal {
		msg := fmt.Sprintf("plugin status not normal: %s", plugin.Status())
		switch plugin.Status() {
		case types.PluginStatusOffline:
			msg = "plugin offline"
		}
		c.JSON(http.StatusBadRequest, gin.H{
			"msg":    msg,
			"status": http.StatusServiceUnavailable,
		})
		return
	}

	pluginPath := c.Param("path")

	headerMap := make(map[string]string)
	for k, v := range c.Request.Header {
		if len(v) == 0 {
			continue
		}
		headerMap[k] = v[0]
	}

	queryMap := make(map[string]string)
	values := c.Request.URL.Query()
	for k, v := range values {
		if len(v) == 0 {
			continue
		}
		queryMap[k] = v[0]
	}

	bodyRead := c.Request.Body

	var (
		body []byte
		err  error
	)
	if bodyRead != nil {
		body, err = io.ReadAll(bodyRead)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
	}

	request := &pluginproto.HttpRequest{
		Method:  c.Request.Method,
		Path:    pluginPath,
		Headers: headerMap,
		Query:   queryMap,
		Body:    body,
	}

	// 请求插件的路由
	timeoutCtx, cancel := context.WithTimeout(c.Request.Context(), time.Second*5)
	resp, err := plugin.Route(timeoutCtx, request)
	cancel()
	if err != nil {
		c.Status(http.StatusInternalServerError)
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	// 处理插件的响应
	c.Status(int(resp.Status))
	for k, v := range resp.Headers {
		c.Writer.Header().Set(k, v)
	}
	_, err = c.Writer.Write(resp.Body)
	if err != nil {
		s.Error("write response error", zap.Error(err), zap.String("plugin", pluginNo), zap.String("path", pluginPath))
	}
}

func (s *Server) handleUpdatePluginConfig(c *wkhttp.Context) {

	if !options.G.Auth.HasPermissionWithContext(c, resource.Plugin.ConfigUpdate, auth.ActionWrite) {
		c.ResponseErrorWithStatus(http.StatusForbidden, errors.New("没有权限"))
		return
	}

	pluginNo := c.Param("plugin")

	var req struct {
		Config map[string]interface{} `json:"config"`
		NodeId uint64                 `json:"node_id"`
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		c.ResponseError(err)
		return
	}

	if req.NodeId == 0 {
		c.ResponseError(fmt.Errorf("node_id is required"))
		return
	}

	node := service.Cluster.NodeInfoById(req.NodeId)
	if node == nil {
		s.Error("node not found", zap.Uint64("node_id", req.NodeId))
		c.ResponseError(fmt.Errorf("node not found"))
		return
	}

	if !options.G.IsLocalNode(node.Id) {
		// 转发到指定节点
		c.ForwardWithBody(fmt.Sprintf("%s%s", node.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	// 获取插件数据
	plugin, err := service.Store.DB().GetPlugin(pluginNo)
	if err != nil {
		s.Error("get plugin failed", zap.Error(err), zap.String("plugin", pluginNo))
		c.ResponseError(err)
		return
	}

	// 合并secret字段的值 (如果请求的配置字段值为******，则使用原来的值)
	if plugin.Config != nil && req.Config != nil {
		for k, v := range req.Config {
			if v == secretHidden {
				if val, ok := plugin.Config[k]; ok {
					req.Config[k] = val
				}
			}
		}
	}

	// 更新插件配置
	err = service.Store.DB().UpdatePluginConfig(pluginNo, req.Config)
	if err != nil {
		s.Error("update plugin config failed", zap.Error(err), zap.String("plugin", pluginNo))
		c.ResponseError(err)
		return
	}

	// 通知插件配置更新
	pluginM := s.pluginManager.get(pluginNo)
	if pluginM != nil {
		if err := pluginM.UpdateConfig(req.Config); err != nil {
			s.Error("update plugin config failed", zap.Error(err), zap.String("plugin", pluginNo))
			c.ResponseError(err)
			return
		}
		if err := pluginM.NotifyConfigUpdate(); err != nil {
			s.Error("notify plugin config update failed", zap.Error(err), zap.String("plugin", pluginNo))
			c.ResponseError(err)
			return
		}
	}
	c.ResponseOK()
}

func (s *Server) handlePluginBind(c *wkhttp.Context) {

	if !options.G.Auth.HasPermissionWithContext(c, resource.PluginUser.Add, auth.ActionWrite) {
		c.ResponseErrorWithStatus(http.StatusForbidden, errors.New("没有权限"))
		return
	}

	var req struct {
		PluginNo string `json:"plugin_no"`
		Uid      string `json:"uid"`
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		c.ResponseError(err)
		return
	}

	if req.PluginNo == "" || req.Uid == "" {
		c.ResponseError(fmt.Errorf("plugin_no and uid is required"))
		return
	}

	leaderId, err := service.Cluster.LeaderIdOfChannel(req.Uid, wkproto.ChannelTypePerson)
	if err != nil {
		s.Error("get leader id failed", zap.Error(err), zap.String("uid", req.Uid))
		c.ResponseError(err)
		return
	}

	if !options.G.IsLocalNode(leaderId) {
		node := service.Cluster.NodeInfoById(leaderId)
		if node == nil {
			s.Error("node not found", zap.Uint64("node_id", leaderId))
			c.ResponseError(fmt.Errorf("node not found"))
			return
		}
		// 转发到指定节点
		c.ForwardWithBody(fmt.Sprintf("%s%s", node.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	err = service.Store.UpdateUserPluginNo(req.Uid, req.PluginNo)
	if err != nil {
		s.Error("update user plugin no failed", zap.Error(err), zap.String("plugin_no", req.PluginNo), zap.String("uid", req.Uid))
		c.ResponseError(err)
		return
	}

	s.removePluginNoFromCache(req.Uid)
	s.removeIsAiFromCache(req.Uid)
	c.ResponseOK()
}

func (s *Server) handlePluginUnbind(c *wkhttp.Context) {

	if !options.G.Auth.HasPermissionWithContext(c, resource.PluginUser.Delete, auth.ActionWrite) {
		c.ResponseErrorWithStatus(http.StatusForbidden, errors.New("没有权限"))
		return
	}

	var req struct {
		PluginNo string `json:"plugin_no"`
		Uid      string `json:"uid"`
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		c.ResponseError(err)
		return
	}

	if req.PluginNo == "" || req.Uid == "" {
		c.ResponseError(fmt.Errorf("plugin_no and uid is required"))
		return
	}

	leaderId, err := service.Cluster.LeaderIdOfChannel(req.Uid, wkproto.ChannelTypePerson)
	if err != nil {
		s.Error("get leader id failed", zap.Error(err), zap.String("uid", req.Uid))
		c.ResponseError(err)
		return
	}

	if !options.G.IsLocalNode(leaderId) {
		node := service.Cluster.NodeInfoById(leaderId)
		if node == nil {
			s.Error("node not found", zap.Uint64("node_id", leaderId))
			c.ResponseError(fmt.Errorf("node not found"))
			return
		}
		// 转发到指定节点
		c.ForwardWithBody(fmt.Sprintf("%s%s", node.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	err = service.Store.RemovePluginUser(req.Uid, req.PluginNo)
	if err != nil {
		s.Error("update user plugin no failed", zap.Error(err), zap.String("plugin_no", req.PluginNo), zap.String("uid", req.Uid))
		c.ResponseError(err)
		return
	}

	s.removePluginNoFromCache(req.Uid)
	s.removeIsAiFromCache(req.Uid)
	c.ResponseOK()
}

func (s *Server) handlePluginBindList(c *wkhttp.Context) {
	uid := c.Query("uid")
	pluginNo := c.Query("plugin_no")
	nodeId := wkutil.ParseUint64(c.Query("node_id"))

	if nodeId != 0 && options.G.IsLocalNode(nodeId) {
		// 本地节点
		pluginUsers, err := s.searchPluginUsers(wkdb.SearchPluginUserReq{
			Uid:      uid,
			PluginNo: pluginNo,
		})
		if err != nil {
			s.Error("search plugin users failed", zap.Error(err))
			c.ResponseError(err)
			return
		}
		c.JSON(http.StatusOK, pluginUsers)
		return
	}

	nodes := service.Cluster.Nodes()
	if len(nodes) == 0 {
		c.ResponseError(fmt.Errorf("not found node"))
		return
	}

	var allResps []*pluginUserResp
	var allRespLock sync.Mutex

	timeoutCtx, cancel := context.WithTimeout(c.Request.Context(), time.Second*5)
	defer cancel()

	eg, _ := errgroup.WithContext(timeoutCtx)

	for _, node := range nodes {
		if options.G.IsLocalNode(node.Id) {
			resps, err := s.searchPluginUsers(wkdb.SearchPluginUserReq{
				Uid:      uid,
				PluginNo: pluginNo,
			})
			if err != nil {
				s.Error("search plugin users failed", zap.Error(err))
				c.ResponseError(err)
				return
			}
			allRespLock.Lock()
			allResps = append(allResps, resps...)
			allRespLock.Unlock()
			continue
		}

		reqUrl := fmt.Sprintf("%s%s", node.ApiServerAddr, c.Request.URL.Path)

		nodeId := node.Id
		if nodeId == 0 {
			s.Error("handlePluginBindList: node id is 0")
			c.ResponseError(fmt.Errorf("node id is 0"))
			return
		}
		eg.Go(func() error {
			pluginUsers, err := s.forwardSearchPluginUsers(reqUrl, nodeId)
			if err != nil {
				return err
			}
			allRespLock.Lock()
			allResps = append(allResps, pluginUsers...)
			allRespLock.Unlock()
			return nil
		})
	}
	err := eg.Wait()
	if err != nil {
		s.Error("search plugin users failed", zap.Error(err))
		c.ResponseError(err)
		return
	}

	// 去重
	uniqueResps := make([]*pluginUserResp, 0, len(allResps))
	uniqueMap := make(map[string]struct{})
	for _, resp := range allResps {
		key := fmt.Sprintf("%s_%s", resp.PluginNo, resp.Uid)
		if _, ok := uniqueMap[key]; ok {
			continue
		}
		uniqueMap[key] = struct{}{}
		uniqueResps = append(uniqueResps, resp)
	}

	sort.Slice(uniqueResps, func(i, j int) bool {

		return uniqueResps[i].CreatedAt > uniqueResps[j].CreatedAt
	})

	c.JSON(http.StatusOK, uniqueResps)
}

func (s *Server) forwardSearchPluginUsers(url string, nodeId uint64) ([]*pluginUserResp, error) {
	resp, err := network.Get(url, map[string]string{
		"node_id": fmt.Sprintf("%d", nodeId),
	}, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}

	var resps []*pluginUserResp
	if err := wkutil.ReadJSONByByte([]byte(resp.Body), &resps); err != nil {
		return nil, err
	}
	return resps, nil
}

func (s *Server) searchPluginUsers(req wkdb.SearchPluginUserReq) ([]*pluginUserResp, error) {
	pluginUsers, err := service.Store.DB().SearchPluginUsers(req)
	if err != nil {
		return nil, err
	}

	resps := make([]*pluginUserResp, 0, len(pluginUsers))
	for _, pu := range pluginUsers {
		resps = append(resps, newPluginUserResp(pu))
	}
	return resps, nil
}

func (s *Server) handleUninstall(c *wkhttp.Context) {

	if !options.G.Auth.HasPermissionWithContext(c, resource.Plugin.Uninstall, auth.ActionWrite) {
		c.ResponseErrorWithStatus(http.StatusForbidden, errors.New("没有权限"))
		return
	}

	var req struct {
		PluginNo string `json:"plugin_no"` // 插件编号
		NodeId   uint64 `json:"node_id"`   // 插件所在节点
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		c.ResponseError(err)
		return
	}

	if req.PluginNo == "" {
		c.ResponseError(fmt.Errorf("plugin_no is required"))
		return
	}

	if req.NodeId == 0 {
		req.NodeId = options.G.Cluster.NodeId
	}

	// 如果插件不是本地节点的插件，则转发到指定节点
	if !options.G.IsLocalNode(req.NodeId) {
		node := service.Cluster.NodeInfoById(req.NodeId)
		if node == nil {
			s.Error("node not found", zap.Uint64("node_id", req.NodeId))
			c.ResponseError(fmt.Errorf("node not found"))
			return
		}
		// 转发到指定节点
		c.ForwardWithBody(fmt.Sprintf("%s%s", node.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	plugin, err := service.Store.DB().GetPlugin(req.PluginNo)
	if err != nil {
		s.Error("get plugin failed", zap.Error(err), zap.String("plugin_no", req.PluginNo))
		c.ResponseError(err)
		return
	}

	if wkdb.IsEmptyPlugin(plugin) {
		c.ResponseError(fmt.Errorf("plugin not found"))
		return
	}

	// 从数据库删除插件
	err = service.Store.DB().DeletePlugin(req.PluginNo)
	if err != nil {
		s.Error("delete plugin failed", zap.Error(err), zap.String("plugin_no", req.PluginNo))
		c.ResponseError(err)
		return
	}

	// 卸载插件
	err = s.UninstallPlugin(plugin.No, plugin.Name)
	if err != nil {
		s.Error("uninstall plugin failed", zap.Error(err), zap.String("plugin_no", req.PluginNo))
		c.ResponseError(err)
		return
	}
	c.ResponseOK()
}
