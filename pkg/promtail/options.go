package promtail

import "time"

type Options struct {
	NodeId     uint64
	Url        string        // loki url example: http://localhost:3100
	LogDir     string        // Log directory
	BatchWait  time.Duration // 间隔多久发送一次日志
	BatchBytes int           // 每次发送日志的大小
	Address    string
	Job        string
}

func NewOptions() *Options {
	return &Options{
		Address: "localhost",
		Job:     "wk",
	}
}
