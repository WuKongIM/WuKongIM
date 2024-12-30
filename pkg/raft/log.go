package raft

import "time"

type Log struct {
	Id    uint64
	Index uint64 // 日志下标
	Term  uint32 // 领导任期
	Data  []byte // 日志数据

	// 不参与编码
	Time time.Time // 日志时间
}
