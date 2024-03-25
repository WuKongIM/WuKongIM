package wkdb

type Options struct {
	DataDir string
}

func NewOptions(opt ...Option) *Options {
	o := &Options{
		DataDir: "./data",
	}
	for _, f := range opt {
		f(o)
	}
	return o
}

type Option func(*Options)

func WithDir(dir string) Option {
	return func(o *Options) {
		o.DataDir = dir
	}
}
