package plugin

import (
	"fmt"
	"os"
	"path"

	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/wkrpc"
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
	return nil
}

func (s *Server) Stop() {
	s.rpcServer.Stop()
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
	pth := path.Join(homeDir, ".wukong", "run", "wukongim.socket")
	return fmt.Sprintf("unix://%s", pth), nil
}
