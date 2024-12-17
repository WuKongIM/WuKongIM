package tag

import (
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/lni/goutils/syncutil"
)

type blucket struct {
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
}

func newBlucket(index int, expire time.Duration) *blucket {

	b := &blucket{
		index:   index,
		expire:  expire,
		stopper: syncutil.NewStopper(),
	}
	b.tag.m = make(map[string]*types.Tag)
	return b
}

func (b *blucket) start() error {
	b.stopper.RunWorker(b.loop)
	return nil
}

func (b *blucket) stop() {
	b.stopper.Stop()
}

func (b *blucket) loop() {
	tk := time.NewTicker(b.expire / 2)
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

func (b *blucket) checkExpireTags() {
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
	}
}

func (b *blucket) setTag(tag *types.Tag) {
	b.tag.Lock()
	b.tag.m[tag.Key] = tag
	b.tag.Unlock()
}

func (b *blucket) getTag(tagKey string) *types.Tag {
	b.tag.RLock()
	defer b.tag.RUnlock()
	return b.tag.m[tagKey]
}

func (b *blucket) removeTag(tagKey string) {
	b.tag.Lock()
	defer b.tag.Unlock()
	delete(b.tag.m, tagKey)
}

func (b *blucket) setChannelTag(channelId string, channelType uint8, tagKey string) {
	b.channel.Lock()
	b.channel.m[wkutil.ChannelToKey(channelId, channelType)] = tagKey
	b.channel.Unlock()
}

func (b *blucket) getChannelTag(channelId string, channelType uint8) string {
	b.channel.RLock()
	defer b.channel.RUnlock()
	return b.channel.m[wkutil.ChannelToKey(channelId, channelType)]
}
