package trace

type Options struct {
	TraceOn bool // 是否开启trace
	// Endpoint is the address of the collector to which the exporter will send the spans.
	Endpoint           string
	ServiceName        string
	ServiceHostName    string
	RequestPoolRunning func() int64
	MessagePoolRunning func() int64

	OutboundFlightMessageCount func() int64
	OutboundFlightMessageBytes func() int64
	InboundFlightMessageCount  func() int64
	InboundFlightMessageBytes  func() int64
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

func WithRequestPoolRunning(f func() int64) Option {
	return func(o *Options) {
		o.RequestPoolRunning = f
	}
}

func WithMessagePoolRunning(f func() int64) Option {
	return func(o *Options) {
		o.MessagePoolRunning = f
	}
}

func WithOutboundFlightMessageCount(f func() int64) Option {
	return func(o *Options) {
		o.OutboundFlightMessageCount = f
	}
}

func WithOutboundFlightMessageBytes(f func() int64) Option {
	return func(o *Options) {
		o.OutboundFlightMessageBytes = f
	}
}

func WithInboundFlightMessageCount(f func() int64) Option {
	return func(o *Options) {
		o.InboundFlightMessageCount = f
	}
}

func WithInboundFlightMessageBytes(f func() int64) Option {
	return func(o *Options) {
		o.InboundFlightMessageBytes = f
	}
}
