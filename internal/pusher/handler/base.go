package handler

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/pkg/fasthash"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	lru "github.com/hashicorp/golang-lru"
)

type Handler struct {
	wklog.Log

	userPluginBuckets []*userPluginBucket
	bucketSize        int
}

func NewHandler() *Handler {
	h := &Handler{
		Log:        wklog.NewWKLog("handler"),
		bucketSize: 10,
	}

	h.userPluginBuckets = make([]*userPluginBucket, h.bucketSize)
	for i := 0; i < h.bucketSize; i++ {
		h.userPluginBuckets[i] = newUserPluginBucket(i)
	}

	h.routes()
	return h
}

func (h *Handler) getPluginNoFromCache(uid string) (string, bool) {
	fh := fasthash.Hash(uid)
	index := int(fh) % h.bucketSize
	return h.userPluginBuckets[index].get(uid)
}

func (h *Handler) setPluginNoToCache(uid, pluginNo string) {
	fh := fasthash.Hash(uid)
	index := int(fh) % h.bucketSize
	h.userPluginBuckets[index].add(uid, pluginNo)
}

func (h *Handler) routes() {

	// 在线推送
	eventbus.RegisterPusherHandlers(eventbus.EventPushOnline, h.pushOnline)
	// 离线推送
	eventbus.RegisterPusherHandlers(eventbus.EventPushOffline, h.pushOffline)
}

// 收到消息
func (h *Handler) OnMessage(m *proto.Message) {

}

// 收到事件
func (h *Handler) OnEvent(ctx *eventbus.PushContext) {
	// 执行本地事件
	eventbus.ExecutePusherEvent(ctx)
}

type userPluginBucket struct {
	cache *lru.Cache
	index int
}

func newUserPluginBucket(index int) *userPluginBucket {
	cache, _ := lru.New(1000)

	return &userPluginBucket{
		cache: cache,
		index: index,
	}
}

func (u *userPluginBucket) add(key string, value string) {
	u.cache.Add(key, value)
}

func (u *userPluginBucket) get(key string) (string, bool) {
	v, ok := u.cache.Get(key)
	if !ok {
		return "", false
	}
	return v.(string), true
}
