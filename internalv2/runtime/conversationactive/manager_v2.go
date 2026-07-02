package conversationactive

import (
	"context"
	"sync/atomic"
	"time"
)

const defaultNumShards = 16

// ManagerV2 是新架构的会话活跃管理器，使用分片缓存降低锁竞争
type ManagerV2 struct {
	// shards 是分片缓存数组（已废弃，使用 hot 替代）
	shards []*CacheShard
	// shardMask 用于快速计算分片索引（numShards - 1）
	shardMask uint32
	// hot 热缓存（带容量限制）
	hot *HotCache
	// cold 冷缓存（已刷盘数据）
	cold *ColdCache
	// dirtyIndex 按 hashSlot 索引脏数据
	dirtyIndex *DirtyIndex
	// store 持久化存储接口
	store ActiveStore
	// nowMS 返回当前时间戳
	nowMS func() int64
	// nextVersion 生成单调递增的版本号
	nextVersion atomic.Uint64
	// observer 观察者接口
	observer Observer
}

// NewManagerV2 创建一个新的 ManagerV2 实例
func NewManagerV2(opts Options) *ManagerV2 {
	nowMS := opts.NowMS
	if nowMS == nil {
		nowMS = func() int64 {
			return time.Now().UnixMilli()
		}
	}

	numShards := defaultNumShards
	shards := make([]*CacheShard, numShards)
	for i := 0; i < numShards; i++ {
		shards[i] = newCacheShard()
	}

	// 默认最大缓存 10 万条目
	maxCachedRows := opts.MaxCachedRows
	if maxCachedRows == 0 {
		maxCachedRows = 100000
	}

	return &ManagerV2{
		shards:     shards,
		shardMask:  uint32(numShards - 1),
		hot:        newHotCache(numShards, maxCachedRows),
		cold:       newColdCache(),
		dirtyIndex: newDirtyIndex(),
		store:      opts.Store,
		nowMS:      nowMS,
		observer:   opts.Observer,
	}
}

// getShard 根据 uid 获取对应的分片
func (m *ManagerV2) getShard(uid string) *CacheShard {
	idx := shardIndex(uid, uint32(len(m.shards)))
	return m.shards[idx]
}

// MarkActive 标记活跃会话（不带 hashSlot）
func (m *ManagerV2) MarkActive(ctx context.Context, patches []ActivePatch) error {
	return m.markActive(ctx, 0, false, patches)
}

// MarkActiveForHashSlot 标记活跃会话（带 hashSlot）
func (m *ManagerV2) MarkActiveForHashSlot(ctx context.Context, hashSlot uint16, patches []ActivePatch) error {
	return m.markActive(ctx, hashSlot, true, patches)
}

func (m *ManagerV2) markActive(ctx context.Context, hashSlot uint16, hasHashSlot bool, patches []ActivePatch) error {
	if len(patches) == 0 {
		return nil
	}

	// 按 uid 分组，减少锁竞争
	groupedByUID := make(map[string][]ActivePatch)
	for _, patch := range patches {
		if patch.UID == "" {
			continue
		}
		groupedByUID[patch.UID] = append(groupedByUID[patch.UID], patch)
	}

	// 逐个 uid 处理
	for uid, userPatches := range groupedByUID {
		for _, patch := range userPatches {
			version := m.nextVersion.Add(1)

			key := conversationKey{
				kind:        patch.Kind,
				channelID:   patch.ChannelID,
				channelType: patch.ChannelType,
			}

			// 先检查热缓存
			existing, ok := m.hot.get(uid, key)

			// 如果不在热缓存，检查冷缓存（冷到热提升）
			if !ok {
				coldPatch, coldOK := m.cold.get(uid, key)
				if coldOK {
					// 从冷缓存提升，合并数据
					if coldPatch.ActiveAtMS > patch.ActiveAtMS {
						patch.ActiveAtMS = coldPatch.ActiveAtMS
					}
					if coldPatch.ReadSeq > patch.ReadSeq {
						patch.ReadSeq = coldPatch.ReadSeq
					}
					// 从冷缓存移除
					m.cold.remove(uid, key)
				}
			}

			// 合并逻辑：取最大值
			merged := patch
			if ok {
				if existing.patch.ActiveAtMS > merged.ActiveAtMS {
					merged.ActiveAtMS = existing.patch.ActiveAtMS
				}
				if existing.patch.ReadSeq > merged.ReadSeq {
					merged.ReadSeq = existing.patch.ReadSeq
				}
			}

			// 设置到热缓存
			m.hot.set(merged, version, hashSlot, hasHashSlot)

			// 标记为脏数据
			if hasHashSlot {
				addr := cacheAddress{
					uid: uid,
					key: key,
				}
				entry := flushEntry{
					uid:     uid,
					key:     key,
					patch:   merged,
					version: version,
				}
				m.dirtyIndex.add(hashSlot, addr, entry)
			}
		}
	}

	return nil
}

// getEntryForTest 是测试辅助方法，获取指定 uid 和 key 的 entry
func (m *ManagerV2) getEntryForTest(uid string, key conversationKey) (*cacheEntry, bool) {
	return m.hot.get(uid, key)
}

// moveHotToCold 将条目从热缓存迁移到冷缓存
func (m *ManagerV2) moveHotToCold(addrs []cacheAddress) {
	for _, addr := range addrs {
		// 从热缓存读取
		entry, ok := m.hot.get(addr.uid, addr.key)
		if !ok {
			continue
		}

		// 移到冷缓存
		m.cold.set(entry.patch)

		// 从热缓存删除
		m.hot.remove(addr.uid, addr.key)
	}
}
