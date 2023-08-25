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
}

func NewOptions() *Options {

	return &Options{
		HeartbeatInterval: time.Second * 60,
		ConnectTimeout:    time.Second * 5,
		Reconnect:         true,
		RequestTimeout:    time.Second * 5,
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
