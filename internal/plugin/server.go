package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/fsnotify/fsnotify"

	"github.com/WuKongIM/WuKongIM/pkg/fasthash"
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

	userPluginBuckets []*userPluginBucket
	bucketSize        int // 缓存桶大小
}

func NewServer(opts *Options) *Server {

	addr, err := getUnixSocket(opts.SocketPath)
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

	// // 插件目录权限为只读，防止插件在目录下创建文件
	// if err = os.Chmod(opts.Dir, os.ModePerm); err != nil {
	// 	return nil
	// }

	s := &Server{
		rpcServer:     rpcServer,
		opts:          opts,
		pluginManager: newPluginManager(),
		Log:           wklog.NewWKLog("plugin.server"),
		sandboxDir:    sandboxDir,
		bucketSize:    10,
	}
	s.rpc = newRpc(s)

	s.userPluginBuckets = make([]*userPluginBucket, s.bucketSize)
	for i := 0; i < s.bucketSize; i++ {
		s.userPluginBuckets[i] = newUserPluginBucket(i)
	}
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

	go func() {
		if err := s.installPluginsIfNeed(); err != nil {
			s.Info("install plugins error", zap.Error(err))
			return
		}
	}()

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

func (s *Server) UserIsAI(uid string) bool {
	isAi, ok := s.isAiFromCache(uid)
	if ok {
		return isAi
	}
	exist, err := service.Store.DB().ExistPluginByUid(uid)
	if err != nil {
		s.Error("查询用户AI插件失败！", zap.Error(err), zap.String("uid", uid))
		return false
	}
	s.setIsAiToCache(uid, exist)
	return exist
}

func (s *Server) GetUserPluginNo(uid string) (string, error) {
	pluginNo, ok := s.getPluginNoFromCache(uid)
	if ok {
		return pluginNo, nil
	}
	pluginNo, err := service.Store.DB().GetHighestPriorityPluginByUid(uid)
	if err != nil {
		s.Error("获取用户AI插件编号失败！", zap.Error(err), zap.String("uid", uid))
		return "", err
	}
	if pluginNo != "" {
		s.setPluginNoToCache(uid, pluginNo)
	}
	return pluginNo, nil
}

func (s *Server) getPluginNoFromCache(uid string) (string, bool) {
	fh := fasthash.Hash(uid)
	index := int(fh) % s.bucketSize
	return s.userPluginBuckets[index].get(uid)
}

func (s *Server) setPluginNoToCache(uid, pluginNo string) {
	fh := fasthash.Hash(uid)
	index := int(fh) % s.bucketSize
	s.userPluginBuckets[index].add(uid, pluginNo)
}

func (s *Server) removePluginNoFromCache(uid string) {
	fh := fasthash.Hash(uid)
	index := int(fh) % s.bucketSize
	s.userPluginBuckets[index].remove(uid)
}

func (s *Server) setIsAiToCache(uid string, isAi bool) {
	fh := fasthash.Hash(uid)
	index := int(fh) % s.bucketSize
	s.userPluginBuckets[index].setIsAi(uid, isAi)
}

func (s *Server) isAiFromCache(uid string) (bool, bool) {
	fh := fasthash.Hash(uid)
	index := int(fh) % s.bucketSize
	return s.userPluginBuckets[index].isAi(uid)
}

func (s *Server) removeIsAiFromCache(uid string) {
	fh := fasthash.Hash(uid)
	index := int(fh) % s.bucketSize
	s.userPluginBuckets[index].removeIsAi(uid)
}

func getUnixSocket(socketPath string) (string, error) {

	err := os.Remove(socketPath)
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

// installPlugins 安装插件
func (s *Server) installPluginsIfNeed() error {
	for _, pluginUrl := range s.opts.Install {
		// 安装插件
		err := s.installPlugin(pluginUrl)
		if err != nil {
			s.Error("install plugin error", zap.Error(err))
			continue
		}

	}
	return nil
}

func (s *Server) installPlugin(pluginUrl string) error {

	// 检查插件地址是否合法
	if pluginUrl == "" {
		return errors.New("plugin url is empty")
	}

	if !isPluginExt(path.Ext(pluginUrl)) {
		return errors.New("plugin url is invalid")
	}

	// 替换url中${xx}的参数
	pluginUrl = replacePluginUrl(pluginUrl)

	// 判断插件是否已经安装
	if s.isInstalledPlugin(path.Base(pluginUrl)) {
		return nil
	}

	// 下载插件
	s.Info("Plugin download", zap.String("plugin", pluginUrl))
	pluginTmpPath, err := s.downloadPlugin(pluginUrl)
	if err != nil {
		s.Info("Plugin download error", zap.Error(err))
		return err
	}

	// 移动插件到插件目录
	pluginName := path.Base(pluginUrl)
	newPluginPath := path.Join(s.opts.Dir, pluginName)
	err = os.Rename(pluginTmpPath, newPluginPath)
	if err != nil {
		return err
	}

	// 设置插件权限
	err = os.Chmod(newPluginPath, os.ModePerm)
	if err != nil {
		return err
	}
	s.Info("Plugin install success", zap.String("plugin", pluginUrl))
	return nil
}

func replacePluginUrl(pluginUrl string) string {
	// 替换url中${xx}的参数
	pluginUrl = os.Expand(pluginUrl, func(key string) string {
		switch key {
		case "os":
			return runtime.GOOS
		case "arch":
			return runtime.GOARCH
		}
		return ""
	})
	return pluginUrl
}

func (s *Server) downloadPlugin(pluginUrl string) (string, error) {
	// 下载插件
	pluginTmpPath := path.Join(os.TempDir(), path.Base(pluginUrl)+"_"+time.Now().Format("20060102150405"))
	err := s.downloadFile(pluginUrl, pluginTmpPath)
	if err != nil {
		return "", err
	}
	return pluginTmpPath, nil
}

func (s *Server) downloadFile(url, filePath string) error {
	// 创建文件
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// 下载文件
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return err
	}
	return nil
}

// 判断插件是否已经安装
func (s *Server) isInstalledPlugin(pluginName string) bool {
	pluginPath := path.Join(s.opts.Dir, pluginName)
	_, err := os.Stat(pluginPath)
	return err == nil
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
		// 判断是否是插件扩展
		if !isPluginExt(path.Ext(file.Name())) {
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

	// 监听插件目录的插件变化
	go func() {
		err = s.watchPlugins()
		if err != nil {
			s.Error("watch plugins error", zap.Error(err))
		}
	}()

	return nil
}

// 监听插件目录的插件变化
func (s *Server) watchPlugins() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	watcher.Add(s.opts.Dir) // 监听插件目录

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			// 判断文件是否是目录
			fileInfo, err := os.Stat(event.Name)
			if err != nil {
				if !os.IsNotExist(err) {
					s.Error("stat file error", zap.Error(err))
					continue
				}
			}
			if fileInfo != nil && fileInfo.IsDir() {
				continue
			}
			// 获取插件名字
			pluginName := path.Base(event.Name)
			// 判断是否是插件扩展
			if !isPluginExt(path.Ext(pluginName)) {
				continue
			}

			if event.Has(fsnotify.Create) { // 新增插件

				// 启动插件
				s.Info("Plugin file created", zap.String("plugin", pluginName))
				err = s.startPluginApp(pluginName)
				if err != nil {
					s.Error("start plugin error", zap.Error(err))
				}

			} else if event.Has(fsnotify.Write) { // 插件更新

				// 重启插件
				s.Info("Plugin file changed", zap.String("plugin", pluginName))
				err = s.restartPlugin(pluginName)
				if err != nil {
					s.Error("restart plugin error", zap.Error(err))
				}
			} else if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) { // 插件删除
				s.Info("Plugin file removed", zap.String("plugin", pluginName))
				err = s.stopPluginApp(pluginName)
				if err != nil {
					s.Error("stop plugin error", zap.Error(err))
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			s.Error("watcher error", zap.Error(err))
		}
	}

}

// 是否是插件扩展
func isPluginExt(ext string) bool {
	return ext == ".wkp"
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

	// 获取SocketPath的绝对路径
	socketPath, err := filepath.Abs(s.opts.SocketPath)
	if err != nil {
		s.Error("get socket path error", zap.Error(err))
		return err
	}
	shadboxDir, err := filepath.Abs(s.sandboxDir)
	if err != nil {
		s.Error("get sandbox dir error", zap.Error(err))
		return err
	}

	// 启动插件
	cmd := exec.Command("./"+name, "--socket", socketPath, "--sandbox", shadboxDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = s.opts.Dir

	// 允许相对路径运行
	if errors.Is(cmd.Err, exec.ErrDot) {
		cmd.Err = nil
	}
	// start the process
	err = cmd.Start()
	if err != nil {
		s.Error("starting plugin process failed", zap.Error(err), zap.String("plugin", name))
		return err
	}

	return nil
}

func (s *Server) restartPlugin(name string) error {
	// 停止插件
	err := s.stopPluginApp(name)
	if err != nil {
		return err
	}

	// 启动插件
	err = s.startPluginApp(name)
	if err != nil {
		return err
	}
	return nil
}

// 停止插件程序
func (s *Server) stopPluginApp(name string) error {

	// 通知插件停止
	plugins := s.pluginManager.getByName(name)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, p := range plugins {
		_ = p.Stop(timeoutCtx)
	}
	// pluginPath := path.Join(s.opts.Dir, name)
	// cmd := exec.Command("pkill", "-f", pluginPath)
	// err := cmd.Run()
	// if err != nil {
	// 	return err
	// }
	return nil
}

func (s *Server) stopPluginAppByPluginNo(no string) error {
	// 通知插件停止
	p := s.pluginManager.get(no)
	if p == nil {
		return nil
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return p.Stop(timeoutCtx)
}

// UninstallPlugin 卸载插件
func (s *Server) UninstallPlugin(pluginNo string, name string) error {
	// 停止插件
	err := s.stopPluginAppByPluginNo(pluginNo)
	if err != nil {
		return err
	}

	// 删除插件
	pluginPath := path.Join(s.opts.Dir, name)

	// 判断文件是否存在
	_, err = os.Stat(pluginPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
	}

	// 删除插件
	err = os.Remove(pluginPath)
	if err != nil {
		return err
	}
	return nil
}
