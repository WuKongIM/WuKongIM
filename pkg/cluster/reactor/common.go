package reactor

import (
	"errors"
	"hash"
	"hash/fnv"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

var (
	ErrReactorSubStopped = errors.New("reactor sub stopped")
	ErrNotLeader         = errors.New("not leader")
	ErrPausePropopose    = errors.New("pause propose")
)

var hashPool = sync.Pool{
	New: func() interface{} {
		return fnv.New32a()
	},
}

func hashWthString(key string) uint32 {
	if strings.TrimSpace(key) == "" {
		wklog.Panic("key is nil", zap.String("key", key))
		return 0
	}
	h := hashPool.Get().(hash.Hash32)
	_, err := h.Write([]byte(key))
	if err != nil {
		h.Reset()
		hashPool.Put(h)
		wklog.Panic("hash string error", zap.String("key", key), zap.Error(err))
		return 0
	}
	num := h.Sum32()
	h.Reset()
	hashPool.Put(h)
	return num
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
