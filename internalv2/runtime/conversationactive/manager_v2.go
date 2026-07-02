package conversationactive

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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
	// flushWorker 异步刷盘协程
	flushWorker *FlushWorker
	// flushInterval 刷盘间隔
	flushInterval time.Duration
	// metrics 运行时指标
	metrics *Metrics
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
		shards:        shards,
		shardMask:     uint32(numShards - 1),
		hot:           newHotCache(numShards, maxCachedRows),
		cold:          newColdCache(),
		dirtyIndex:    newDirtyIndex(),
		store:         opts.Store,
		nowMS:         nowMS,
		observer:      opts.Observer,
		flushInterval: opts.FlushInterval,
		metrics:       NewMetrics(),
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

// StartFlushWorker 启动异步刷盘协程
func (m *ManagerV2) StartFlushWorker() {
	if m.flushWorker == nil {
		m.flushWorker = newFlushWorker(m, m.flushInterval)
	}
	m.flushWorker.Start()
}

// StopFlushWorker 停止异步刷盘协程
func (m *ManagerV2) StopFlushWorker() {
	if m.flushWorker != nil {
		m.flushWorker.Stop()
	}
}

// SignalFlush 触发立即刷盘
func (m *ManagerV2) SignalFlush() {
	if m.flushWorker != nil {
		m.flushWorker.Signal()
	}
}

// GetMetrics 获取当前运行时指标
func (m *ManagerV2) GetMetrics() MetricsSnapshot {
	return m.metrics.GetSnapshot()
}

// ListActiveView 查询活跃会话列表（三路合并：热+冷+DB）
func (m *ManagerV2) ListActiveView(ctx context.Context, kind metadb.ConversationKind, uid string, after metadb.ConversationActiveCursor, limit int) (ActiveViewPage, error) {
	var allRows []metadb.ConversationState

	// 1. 从热缓存收集
	hotRows := m.collectFromHot(uid, kind)
	allRows = append(allRows, hotRows...)

	// 2. 从冷缓存收集
	coldRows := m.collectFromCold(uid, kind)
	allRows = append(allRows, coldRows...)

	// 3. 从 DB 收集（如果有 store）
	if m.store != nil {
		dbRows, _, _, err := m.store.ListConversationActivePage(ctx, kind, uid, after, limit*2)
		if err == nil {
			allRows = append(allRows, dbRows...)
		}
	}

	// 去重和排序（按 ActiveAt 降序）
	allRows = m.deduplicateAndSort(allRows)

	// 分页
	if len(allRows) > limit {
		allRows = allRows[:limit]
	}

	// 构造返回结果
	var cursor metadb.ConversationActiveCursor
	done := len(allRows) < limit
	if len(allRows) > 0 {
		last := allRows[len(allRows)-1]
		cursor = metadb.ConversationActiveCursor{
			ActiveAt:    last.ActiveAt,
			ChannelID:   last.ChannelID,
			ChannelType: last.ChannelType,
		}
	}

	return ActiveViewPage{
		Rows:   allRows,
		Cursor: cursor,
		Done:   done,
	}, nil
}

func (m *ManagerV2) collectFromHot(uid string, kind metadb.ConversationKind) []metadb.ConversationState {
	var rows []metadb.ConversationState

	// 遍历所有分片查找该用户的数据
	for _, shard := range m.hot.shards {
		shard.mu.RLock()
		byChannel := shard.entries[uid]
		if byChannel != nil {
			for key, entry := range byChannel {
				if key.kind == kind {
					rows = append(rows, metadb.ConversationState{
						UID:         uid,
						Kind:        kind,
						ChannelID:   key.channelID,
						ChannelType: int64(key.channelType),
						ActiveAt:    entry.patch.ActiveAtMS,
						ReadSeq:     entry.patch.ReadSeq,
					})
				}
			}
		}
		shard.mu.RUnlock()
	}

	return rows
}

func (m *ManagerV2) collectFromCold(uid string, kind metadb.ConversationKind) []metadb.ConversationState {
	var rows []metadb.ConversationState

	m.cold.mu.RLock()
	defer m.cold.mu.RUnlock()

	for addr, patch := range m.cold.entries {
		if addr.uid == uid && addr.key.kind == kind {
			rows = append(rows, metadb.ConversationState{
				UID:         uid,
				Kind:        kind,
				ChannelID:   addr.key.channelID,
				ChannelType: int64(addr.key.channelType),
				ActiveAt:    patch.ActiveAtMS,
				ReadSeq:     patch.ReadSeq,
			})
		}
	}

	return rows
}

func (m *ManagerV2) deduplicateAndSort(rows []metadb.ConversationState) []metadb.ConversationState {
	if len(rows) == 0 {
		return rows
	}

	// 去重：使用 map 保留最新的
	unique := make(map[string]metadb.ConversationState)
	for _, row := range rows {
		key := fmt.Sprintf("%s|%s|%d", row.UID, row.ChannelID, row.ChannelType)

		existing, ok := unique[key]
		if !ok || row.ActiveAt > existing.ActiveAt {
			unique[key] = row
		}
	}

	// 转换回切片
	result := make([]metadb.ConversationState, 0, len(unique))
	for _, row := range unique {
		result = append(result, row)
	}

	// 按 ActiveAt 降序排序（冒泡排序，简化实现）
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].ActiveAt < result[j].ActiveAt {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result
}
