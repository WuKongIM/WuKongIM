package raft

import (
	"crypto/rand"
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
	roleSwitching bool   // 角色切换中
	emptySyncTick int    // 空同步计时器(连续多少次tick没有同步到数据)
	suspend       bool   // 是否挂起
}
