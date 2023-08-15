package transporter

import (
	"time"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type Options struct {
	NodeID       uint64
	Token        string
	proto        wkproto.Protocol
	ConnIdleTime time.Duration
}

func NewOptions() *Options {
	return &Options{
		proto:        wkproto.New(),
		ConnIdleTime: time.Minute * 3,
	}
}

type Option func(opt *Options)

func WithToken(token string) Option {

	return func(opt *Options) {
		opt.Token = token
	}
}
