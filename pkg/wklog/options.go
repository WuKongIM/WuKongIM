package wklog

import "go.uber.org/zap/zapcore"

type Options struct {
	NodeId  uint64
	Level   zapcore.Level
	LogDir  string
	LineNum bool
	Loki    struct {
		Url      string
		Username string
		Password string
	}
}

func NewOptions() *Options {

	return &Options{}
}
