package trace

type Options struct {
	TraceOn bool // 是否开启trace
	// Endpoint is the address of the collector to which the exporter will send the spans.
	Endpoint        string
	ServiceName     string
	ServiceHostName string
}

func NewOptions(opt ...Option) *Options {
	opts := &Options{
		TraceOn:         false,
		Endpoint:        "127.0.0.1:4318",
		ServiceName:     "wukongim",
		ServiceHostName: "wukongim",
	}

	for _, o := range opt {
		o(opts)
	}

	return opts
}

type Option func(*Options)

func WithEndpoint(endpoint string) Option {
	return func(o *Options) {
		o.Endpoint = endpoint
	}
}

func WithServiceName(name string) Option {
	return func(o *Options) {
		o.ServiceName = name
	}
}

func WithServiceHostName(name string) Option {
	return func(o *Options) {
		o.ServiceHostName = name
	}
}
