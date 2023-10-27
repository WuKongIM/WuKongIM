package client

import (
	"time"
)

type Options struct {
	Addr              string
	HeartbeatInterval time.Duration
	ConnectTimeout    time.Duration
	Reconnect         bool
	RequestTimeout    time.Duration
	UID               string
	Token             string
	DefaultBufSize    int // The size of the bufio reader/writer on top of the socket.
	// ReconnectBufSize is the size of the backing bufio during reconnect.
	// Once this has been exhausted publish operations will return an error.
	// Defaults to 8388608 bytes (8MB).
	ReconnectBufSize int
	// FlusherTimeout is the maximum time to wait for write operations
	// to the underlying connection to complete (including the flusher loop).
	FlusherTimeout time.Duration

	// Timeout sets the timeout for a Dial operation on a connection.
	Timeout time.Duration

	PingInterval time.Duration
}

func NewOptions() *Options {

	return &Options{
		HeartbeatInterval: time.Second * 60,
		ConnectTimeout:    time.Second * 5,
		Reconnect:         true,
		RequestTimeout:    time.Second * 5,
		DefaultBufSize:    32768,
		ReconnectBufSize:  8 * 1024 * 1024,
		Timeout:           2 * time.Second,
		PingInterval:      2 * time.Minute,
	}
}

type Option func(opts *Options)

func WithUID(uid string) Option {
	return func(opts *Options) {
		opts.UID = uid
	}
}

func WithToken(token string) Option {
	return func(opts *Options) {
		opts.Token = token
	}
}

func WithConnecTimeout(v time.Duration) Option {
	return func(opts *Options) {
		opts.ConnectTimeout = v
	}
}
