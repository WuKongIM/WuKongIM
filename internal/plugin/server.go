package plugin

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/internal/types/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/wkrpc"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type Server struct {
	rpcServer     *wkrpc.Server
	pluginManager *pluginManager
	api           *api
	wklog.Log
}

func NewServer() *Server {

	addr, err := getUnixSocket()
	if err != nil {
		panic(err)
	}
	rpcServer := wkrpc.New(addr)

	s := &Server{
		rpcServer:     rpcServer,
		pluginManager: newPluginManager(),
		Log:           wklog.NewWKLog("plugin.server"),
	}
	s.api = newApi(s)
	return s
}

func (s *Server) Start() error {
	if err := s.rpcServer.Start(); err != nil {
		return err
	}
	s.api.routes()
	return nil
}

func (s *Server) Stop() {
	s.rpcServer.Stop()
}

func (s *Server) SetRoute(r *wkhttp.WKHttp) {

	r.Any("/plugins/:plugin/*path", s.handlePluginRoute)
}

// 获取插件列表
func (s *Server) Plugins(methods ...types.PluginMethod) []types.Plugin {
	if len(methods) == 0 {
		plugins := s.pluginManager.all()
		results := make([]types.Plugin, 0, len(plugins))
		for _, p := range plugins {
			results = append(results, p)
		}
		return results
	}
	plugins := s.pluginManager.all()
	results := make([]types.Plugin, 0, len(plugins))
	for _, p := range plugins {
		for _, m := range methods {
			if p.hasMethod(m) {
				results = append(results, p)
				break
			}
		}
	}
	return results
}

func getUnixSocket() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	socketPath := path.Join(homeDir, ".wukong", "run", "wukongim.sock")

	err = os.Remove(socketPath)
	if err != nil && !os.IsNotExist(err) {
		log.Printf(`removing "%s": %s`, socketPath, err)
		panic(err)
	}

	socketDir := path.Dir(socketPath)

	err = os.MkdirAll(socketDir, 0777)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("unix://%s", socketPath), nil
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

	fmt.Println("resp--->", resp.Status)

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
