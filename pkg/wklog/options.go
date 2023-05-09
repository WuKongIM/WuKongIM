package wklog

import "go.uber.org/zap/zapcore"

type Options struct {
	Level   zapcore.Level
	LogDir  string
	LineNum bool
}

func NewOptions() *Options {

	return &Options{}
}
