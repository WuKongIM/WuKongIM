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
}

func NewOptions() *Options {

	return &Options{
		HeartbeatInterval: time.Second * 60,
		ConnectTimeout:    time.Second * 5,
		Reconnect:         true,
		RequestTimeout:    time.Second * 5,
	}
}
