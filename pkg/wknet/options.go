package wknet

import (
	"runtime"
	"time"

	"github.com/WuKongIM/crypto/tls"
)

type Mode int // 引擎模式

type Options struct {
	// Addr is the listen addr  example: tcp://127.0.0.1:5100
	Addr string
	// TcpTlsConfig tcp tls config
	TCPTLSConfig *tls.Config
	WSTLSConfig  *tls.Config
	// WsAddr is the listen addr  example: ws://127.0.0.1:5200或 wss://127.0.0.1:5200
	WsAddr  string
	WssAddr string // wss addr
	// WSTlsConfig ws tls config
	// MaxOpenFiles is the maximum number of open files that the server can
	MaxOpenFiles int
	// SubReactorNum is sub reactor numver it's set to runtime.NumCPU()  by default
	SubReactorNum int
	// OnCreateConn allow custom conn
	// ReadBuffSize is the read size of the buffer each time from the connection
	ReadBufferSize int
	// MaxWriteBufferSize is the write maximum size of the buffer for each connection
	MaxWriteBufferSize int
	// MaxReadBufferSize is the read maximum size of the buffer for each connection
	MaxReadBufferSize int
	// SocketRecvBuffer sets the maximum socket receive buffer in bytes.
	SocketRecvBuffer int
	// SocketSendBuffer sets the maximum socket send buffer in bytes.
	SocketSendBuffer int
	// TCPKeepAlive sets up a duration for (SO_KEEPALIVE) socket option.
	TCPKeepAlive time.Duration

	Event struct {
		OnReadBytes  func(n int) // 读到的字节大小
		OnWirteBytes func(n int) // 写出字节大小
	}
}

func NewOptions() *Options {
	return &Options{
		Addr:               "tcp://127.0.0.1:5100",
		MaxOpenFiles:       GetMaxOpenFiles(),
		SubReactorNum:      runtime.NumCPU(),
		ReadBufferSize:     1024 * 32,
		MaxWriteBufferSize: 1024 * 1024 * 50,
		MaxReadBufferSize:  1024 * 1024 * 50,
	}
}

type Option func(opts *Options)

// WithAddr set listen addr
func WithAddr(v string) Option {
	return func(opts *Options) {
		opts.Addr = v
	}
}

func WithWSAddr(v string) Option {
	return func(opts *Options) {
		opts.WsAddr = v
	}
}

func WithWSSAddr(v string) Option {
	return func(opts *Options) {
		opts.WssAddr = v
	}
}

func WithTCPTLSConfig(v *tls.Config) Option {
	return func(opts *Options) {
		opts.TCPTLSConfig = v
	}
}

func WithWSTLSConfig(v *tls.Config) Option {
	return func(opts *Options) {
		opts.WSTLSConfig = v
	}
}

// WithMaxOpenFiles the maximum number of open files that the server can
func WithMaxOpenFiles(v int) Option {
	return func(opts *Options) {
		opts.MaxOpenFiles = v
	}
}

// WithSubReactorNum set sub reactor number
func WithSubReactorNum(v int) Option {
	return func(opts *Options) {
		opts.SubReactorNum = v
	}
}

// WithSocketRecvBuffer sets the maximum socket receive buffer in bytes.
func WithSocketRecvBuffer(recvBuf int) Option {
	return func(opts *Options) {
		opts.SocketRecvBuffer = recvBuf
	}
}

// WithSocketSendBuffer sets the maximum socket send buffer in bytes.
func WithSocketSendBuffer(sendBuf int) Option {
	return func(opts *Options) {
		opts.SocketSendBuffer = sendBuf
	}
}

// WithTCPKeepAlive sets up a duration for (SO_KEEPALIVE) socket option.
func WithTCPKeepAlive(v time.Duration) Option {
	return func(opts *Options) {
		opts.TCPKeepAlive = v
	}
}

func WithOnReadBytes(f func(n int)) Option {
	return func(opts *Options) {
		opts.Event.OnReadBytes = f
	}
}

func WithOnWirteBytes(f func(n int)) Option {

	return func(opts *Options) {
		opts.Event.OnWirteBytes = f
	}
}
