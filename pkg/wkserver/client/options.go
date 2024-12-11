package client

type Options struct {
	Addr  string
	Uid   string
	Token string

	HeartbeatTick        int // 心跳间隔tick数
	HeartbeatTimeoutTick int // 心跳超时tick数

	LogDetailOn bool // 是否开启详细日志
}

func NewOptions(opt ...Option) *Options {
	return &Options{
		HeartbeatTick:        10,
		HeartbeatTimeoutTick: 20,
	}
}

type Option func(*Options)

func WithUid(uid string) Option {
	return func(opts *Options) {
		opts.Uid = uid
	}
}

func WithToken(token string) Option {
	return func(opts *Options) {
		opts.Token = token
	}
}
