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
	OnRequest       func(conn wknet.Conn, req *proto.Request)
	OnResponse      func(conn wknet.Conn, resp *proto.Response)
}

func NewOptions() *Options {

	return &Options{
		Addr:            "tcp://0.0.0.0:12000",
		RequestPoolSize: 20000,
		MessagePoolSize: 10000,
		MessagePoolOn:   true,
		ConnPath:        "/conn",
		ClosePath:       "/close",
		RequestTimeout:  1 * time.Minute,
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

func WithMessagePoolOn(on bool) Option {
	return func(o *Options) {
		o.MessagePoolOn = on
	}
}

func WithAddr(addr string) Option {
	return func(o *Options) {
		o.Addr = addr
	}
}

func WithConnPath(path string) Option {
	return func(o *Options) {
		o.ConnPath = path
	}
}

func WithClosePath(path string) Option {
	return func(o *Options) {
		o.ClosePath = path
	}
}

func WithMaxIdle(maxIdle time.Duration) Option {
	return func(o *Options) {
		o.MaxIdle = maxIdle
	}
}

func WithTimingWheelTick(tick time.Duration) Option {
	return func(o *Options) {
		o.TimingWheelTick = tick
	}
}

func WithTimingWheelSize(size int64) Option {
	return func(o *Options) {
		o.TimingWheelSize = size
	}
}

func WithOnMessage(onMessage func(conn wknet.Conn, msg *proto.Message)) Option {
	return func(o *Options) {
		o.OnMessage = onMessage
	}
}

func WithOnRequest(onRequest func(conn wknet.Conn, req *proto.Request)) Option {
	return func(o *Options) {
		o.OnRequest = onRequest
	}
}

func WithOnResponse(onResponse func(conn wknet.Conn, resp *proto.Response)) Option {
	return func(o *Options) {
		o.OnResponse = onResponse
	}
}
