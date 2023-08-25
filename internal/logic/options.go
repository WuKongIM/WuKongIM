package logic

type Options struct {
	Addr string
}

func NewOptions() *Options {

	return &Options{
		Addr: "tcp://127.0.0.1:12000",
	}
}

type Option func(opts *Options)

func WithAddr(addr string) Option {
	return func(opts *Options) {
		opts.Addr = addr
	}
}
