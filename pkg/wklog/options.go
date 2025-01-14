package wklog

import "go.uber.org/zap/zapcore"

type Options struct {
	NodeId   uint64
	Level    zapcore.Level
	LogDir   string
	LineNum  bool
	TraceOn  bool
	NoStdout bool
}

func NewOptions() *Options {

	return &Options{}
}
