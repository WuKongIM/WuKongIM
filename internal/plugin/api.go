package plugin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/internal/types/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (s *Server) SetRoute(r *wkhttp.WKHttp) {

	r.Any("/plugins/:plugin/*path", s.handlePluginRoute) // 处理插件的路由，将http请求转发给插件

	r.GET("/plugins", s.handleGetPlugins) // 获取插件列表
}

// 获取插件列表

func (s *Server) handleGetPlugins(c *wkhttp.Context) {
	plugins := s.pluginManager.all()

	resps := make([]*pluginResp, 0, len(plugins))
	for _, p := range plugins {
		resps = append(resps, &pluginResp{
			PluginInfo: p.info,
		})
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

type pluginResp struct {
	*pluginproto.PluginInfo
}
