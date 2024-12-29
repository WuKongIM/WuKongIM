package manager

import (
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/lni/goutils/syncutil"
	"github.com/valyala/fastrand"
	"go.uber.org/zap"
)

type tagBlucket struct {
	tag struct {
		m map[string]*types.Tag
		sync.RWMutex
	}
	index   int
	expire  time.Duration
	channel struct {
		sync.RWMutex
		m map[string]string
	}
	stopper *syncutil.Stopper
	wklog.Log
}

func newTagBlucket(index int, expire time.Duration) *tagBlucket {

	b := &tagBlucket{
		index:   index,
		expire:  expire,
		stopper: syncutil.NewStopper(),
		Log:     wklog.NewWKLog("tagBlucket"),
	}
	b.channel.m = make(map[string]string)
	b.tag.m = make(map[string]*types.Tag)
	return b
}

func (b *tagBlucket) start() error {
	b.stopper.RunWorker(b.loop)
	return nil
}

func (b *tagBlucket) stop() {
	b.stopper.Stop()
}

func (b *tagBlucket) loop() {

	scanInterval := b.expire / 2

	p := float64(fastrand.Uint32()) / (1 << 32)
	// 以避免系统中因定时器、周期性任务或请求间隔完全一致而导致的同步问题（例如拥堵或资源竞争）。
	jitter := time.Duration(p * float64(scanInterval))
	tk := time.NewTicker(scanInterval + jitter)
	defer tk.Stop()
	for {
		select {
		case <-tk.C:
			b.checkExpireTags()
		case <-b.stopper.ShouldStop():
			return
		}
	}
}

func (b *tagBlucket) checkExpireTags() {
	b.tag.Lock()
	defer b.tag.Unlock()

	var removeTags []string // 需要移除的tagKey
	for _, tag := range b.tag.m {
		if time.Since(tag.LastGetTime) > b.expire {
			if removeTags == nil {
				removeTags = make([]string, 0, 20)
			}
			removeTags = append(removeTags, tag.Key)
		}
	}

	if len(removeTags) > 0 {
		for _, removeTagKey := range removeTags {
			delete(b.tag.m, removeTagKey)
		}
		b.Info("checkExpireTags: remove tags", zap.Int("count", len(removeTags)), zap.String("removeTag", removeTags[0]))

	}
}

func (b *tagBlucket) setTag(tag *types.Tag) {
	b.tag.Lock()
	b.tag.m[tag.Key] = tag
	b.tag.Unlock()
}

func (b *tagBlucket) getTag(tagKey string) *types.Tag {
	b.tag.RLock()
	defer b.tag.RUnlock()
	return b.tag.m[tagKey]
}

func (b *tagBlucket) removeTag(tagKey string) {
	b.tag.Lock()
	defer b.tag.Unlock()
	delete(b.tag.m, tagKey)
}

func (b *tagBlucket) setChannelTag(channelId string, channelType uint8, tagKey string) {
	b.channel.Lock()
	b.channel.m[wkutil.ChannelToKey(channelId, channelType)] = tagKey
	b.channel.Unlock()
}

func (b *tagBlucket) getChannelTag(channelId string, channelType uint8) string {
	b.channel.RLock()
	defer b.channel.RUnlock()
	return b.channel.m[wkutil.ChannelToKey(channelId, channelType)]
}
