package raft

import (
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"sync"
)

var globalRand = &lockedRand{}

type lockedRand struct {
	mu sync.Mutex
}

func (r *lockedRand) Intn(n int) int {
	r.mu.Lock()
	v, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))
	r.mu.Unlock()
	return int(v.Int64())
}

const (
	None uint64 = 0
	All  uint64 = math.MaxUint64 - 1
)

type SyncInfo struct {
	LastSyncIndex uint64 //最后一次来同步日志的下标（最新日志 + 1）
	StoredIndex   uint64 // 副本已存储的日志下标
	SyncTick      int    // 同步计时器
	GetingLogs    bool   // 领导是否正在查询此副本的日志中
}

type Reason uint8

const (
	ReasonUnknown Reason = iota
	// ReasonOk 同意
	ReasonOk
	// ReasonError 错误
	ReasonError
	// ReasonTrunctate 日志需要截断
	ReasonTrunctate
	// ReasonOnlySync 只是同步, 不做截断判断
	ReasonOnlySync
)

func (r Reason) Uint8() uint8 {
	return uint8(r)
}

func (r Reason) String() string {
	switch r {
	case ReasonOk:
		return "ReasonOk"
	case ReasonError:
		return "ReasonError"
	case ReasonTrunctate:
		return "ReasonTrunctate"
	case ReasonOnlySync:
		return "ReasonOnlySync"
	default:
		return fmt.Sprintf("ReasonUnknown[%d]", r)
	}
}
