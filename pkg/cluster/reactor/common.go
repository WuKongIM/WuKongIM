package reactor

import (
	"errors"
	"hash/fnv"
)

var (
	ErrReactorSubStopped = errors.New("reactor sub stopped")
	ErrNotLeader         = errors.New("not leader")
	ErrPausePropopose    = errors.New("pause propose")
)

func hashWthString(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))

	return h.Sum32()
}

type syncStatus int

const (
	syncStatusNone    syncStatus = iota // 无状态
	syncStatusSyncing                   // 同步中
	syncStatusSynced                    // 已同步
)

const (
	// LevelFastTick 如果指定次tick都没有收到提按则降速为normal
	LevelFastTick int = 100
	LevelNormal   int = 200
	LevelMiddle   int = 300
	LevelSlow     int = 400
	LevelSlowest  int = 500
	LevelDestroy  int = 1200
)
