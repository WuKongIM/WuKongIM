package raftgroup

import "time"

type Options struct {
	TickInterval time.Duration
}

func NewOptions(opt ...Option) *Options {
	os := &Options{
		TickInterval: 100 * time.Millisecond,
	}
	for _, o := range opt {
		o(os)
	}

	return os
}

type Option func(*Options)
