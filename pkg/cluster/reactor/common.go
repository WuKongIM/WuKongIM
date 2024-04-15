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
	fnv.New32a()
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

type requestHandlerAdd struct {
	key     string
	handler *handler
	resultC chan struct{}
}

func newRequestHandlerAdd(key string, handler *handler, resultC chan struct{}) *requestHandlerAdd {
	return &requestHandlerAdd{
		key:     key,
		handler: handler,
		resultC: resultC,
	}
}

type requestHandlerGet struct {
	key     string
	resultC chan IHandler
}

func newRequestHandlerGet(key string, resultC chan IHandler) *requestHandlerGet {
	return &requestHandlerGet{
		key:     key,
		resultC: resultC,
	}
}

type requestHandlerRemove struct {
	key     string
	resultC chan *handler
}

type syncStatus int

const (
	syncStatusNone    syncStatus = iota // 无状态
	syncStatusSyncing                   // 同步中
	syncStatusSynced                    // 已同步
)
