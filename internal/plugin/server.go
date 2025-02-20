package plugin

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"

	"github.com/WuKongIM/WuKongIM/internal/types"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/wkrpc"
	"go.uber.org/zap"
)

type Server struct {
	rpcServer     *wkrpc.Server
	pluginManager *pluginManager
	rpc           *rpc
	wklog.Log
	opts       *Options
	sandboxDir string // 沙箱目录
}

func NewServer(opts *Options) *Server {

	addr, err := getUnixSocket()
	if err != nil {
		panic(err)
	}
	rpcServer := wkrpc.New(addr)

	// 如果插件目录不存在则创建
	if _, err := os.Stat(opts.Dir); os.IsNotExist(err) {
		err := os.MkdirAll(opts.Dir, os.ModePerm)
		if err != nil {
			panic(err)
		}
	}

	// 如果沙箱目录不存在则创建
	sandboxDir := path.Join(opts.Dir, "plugindata")
	if _, err := os.Stat(sandboxDir); os.IsNotExist(err) {
		err := os.MkdirAll(sandboxDir, os.ModePerm)
		if err != nil {
			panic(err)
		}
	}

	// 插件目录权限为只读，防止插件在目录下创建文件
	if err = os.Chmod(opts.Dir, 0555); err != nil {
		return nil
	}

	s := &Server{
		rpcServer:     rpcServer,
		opts:          opts,
		pluginManager: newPluginManager(),
		Log:           wklog.NewWKLog("plugin.server"),
		sandboxDir:    sandboxDir,
	}
	s.rpc = newRpc(s)
	return s
}

func (s *Server) Start() error {
	if err := s.rpcServer.Start(); err != nil {
		return err
	}
	s.rpc.routes()

	if err := s.startPlugins(); err != nil {
		s.Error("start plugins error", zap.Error(err))
		return err
	}

	return nil
}

func (s *Server) Stop() {
	s.rpcServer.Stop()

	s.stopPlugins()
}

// Plugins 获取插件列表
func (s *Server) Plugins(methods ...types.PluginMethod) []types.Plugin {
	if len(methods) == 0 {
		plugins := s.pluginManager.all()
		results := make([]types.Plugin, 0, len(plugins))
		for _, p := range plugins {
			if p.Status() != types.PluginStatusNormal {
				continue
			}
			results = append(results, p)
		}
		return results
	}
	plugins := s.pluginManager.all()
	results := make([]types.Plugin, 0, len(plugins))
	for _, p := range plugins {
		if p.Status() != types.PluginStatusNormal {
			continue
		}
		for _, m := range methods {
			if p.hasMethod(m) {
				results = append(results, p)
				break
			}
		}
	}
	return results
}

// Plugin 获取插件
func (s *Server) Plugin(no string) types.Plugin {
	pg := s.pluginManager.get(no)
	if pg == nil {
		return nil
	}
	return pg
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

// 启动插件执行文件
func (s *Server) startPlugins() error {
	pluginDir := s.opts.Dir
	// 获取插件目录下的所有文件
	files, err := os.ReadDir(pluginDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// 启动插件
		s.Info("Plugin start", zap.String("plugin", file.Name()))
		err := s.startPluginApp(file.Name())
		if err != nil {
			s.Error("start plugin error", zap.Error(err))
			continue
		}
	}
	return nil
}

func (s *Server) stopPlugins() error {
	pluginDir := s.opts.Dir
	// 获取插件目录下的所有文件
	files, err := os.ReadDir(pluginDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// 停止插件
		err := s.stopPluginApp(file.Name())
		if err != nil {
			s.Error("stop plugin error", zap.Error(err))
			continue
		}
	}
	return nil
}

// 启动插件程序
func (s *Server) startPluginApp(name string) error {

	cmd := exec.Command("./" + name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = s.opts.Dir

	// 允许相对路径运行
	if errors.Is(cmd.Err, exec.ErrDot) {
		cmd.Err = nil
	}
	// start the process
	err := cmd.Start()
	if err != nil {
		s.Error("starting plugin process failed", zap.Error(err), zap.String("plugin", name))
		return err
	}
	fmt.Println("pluginPath-222->", name)

	return nil
}

// 停止插件程序
func (s *Server) stopPluginApp(name string) error {
	pluginPath := path.Join(s.opts.Dir, name)
	cmd := exec.Command("pkill", "-f", pluginPath)
	err := cmd.Run()
	if err != nil {
		return err
	}
	return nil
}
