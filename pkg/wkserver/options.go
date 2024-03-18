package wkserver

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type Options struct {
	Addr            string
	RequestPoolSize int  // The size of the request pool, the default is 10000
	MessagePoolSize int  // The size of the message pool, the default is 10000
	MessagePoolOn   bool // Whether to open the message pool, the default is true
	ConnPath        string
	ClosePath       string
	RequestTimeout  time.Duration
	OnMessage       func(conn wknet.Conn, msg *proto.Message)
	MaxIdle         time.Duration

	TimingWheelTick time.Duration // The time-round training interval must be 1ms or more
	TimingWheelSize int64         // Time wheel size
}

func NewOptions() *Options {

	return &Options{
		Addr:            "tcp://0.0.0.0:12000",
		RequestPoolSize: 10000,
		MessagePoolSize: 1000,
		MessagePoolOn:   true,
		ConnPath:        "/conn",
		ClosePath:       "/close",
		RequestTimeout:  10 * time.Second,
		MaxIdle:         120 * time.Second,
		TimingWheelTick: time.Millisecond * 10,
		TimingWheelSize: 100,
	}
}

type Option func(*Options)

func WithRequestTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		o.RequestTimeout = timeout
	}
}

func WithRequestPoolSize(size int) Option {
	return func(o *Options) {
		o.RequestPoolSize = size
	}
}

func WithMessagePoolSize(size int) Option {
	return func(o *Options) {
		o.MessagePoolSize = size
	}
}
