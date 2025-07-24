package plugin

import (
	"os"
	"path"
)

type Options struct {
	Dir        string   // 插件目录
	SocketPath string   // 通信socket文件路径
	Install    []string // 需要安装的插件地址列表
}

func NewOptions(opt ...Option) *Options {

	homeDir, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	socketPath := path.Join(homeDir, ".wukong", "run", "wukongim.sock")

	opts := &Options{
		Dir:        "./plugindir",
		SocketPath: socketPath,
	}
	for _, f := range opt {
		f(opts)
	}

	return opts
}

type Option func(*Options)

func WithDir(dir string) Option {
	return func(o *Options) {
		o.Dir = dir
	}
}

func WithSocketPath(socketPath string) Option {
	return func(o *Options) {
		o.SocketPath = socketPath
	}
}

func WithInstall(install []string) Option {
	return func(o *Options) {
		o.Install = install
	}
}
