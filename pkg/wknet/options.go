package wknet

import (
	"runtime"
	"syscall"
	"time"
)

type Options struct {
	// Addr is the listen addr  example: tcp://127.0.0.1:7677
	Addr string
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
}

func NewOptions() *Options {
	return &Options{
		Addr:               "tcp://127.0.0.1:5100",
		MaxOpenFiles:       getMaxOpenFiles(),
		SubReactorNum:      runtime.NumCPU(),
		ReadBufferSize:     1024 * 32,
		MaxWriteBufferSize: 1024 * 1024 * 50,
		MaxReadBufferSize:  1024 * 1024 * 5,
	}
}

type Option func(opts *Options)

// WithAddr set listen addr
func WithAddr(v string) Option {
	return func(opts *Options) {
		opts.Addr = v
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

func getMaxOpenFiles() int {
	maxOpenFiles := 1024 * 1024 * 2
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err == nil {
		if n := int(limit.Max); n > 0 && n < maxOpenFiles {
			maxOpenFiles = n
		}
	}

	return maxOpenFiles
}
