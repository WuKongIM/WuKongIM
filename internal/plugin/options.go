package plugin

type Options struct {
	Dir string // 插件目录
}

func NewOptions(opt ...Option) *Options {
	opts := &Options{
		Dir: "./plugindir",
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
