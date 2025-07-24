package wkdb

import (
	"crypto/md5"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	lru "github.com/hashicorp/golang-lru/v2"
	"go.uber.org/zap"
)

// ConversationCache 会话缓存 - 专注于 GetLastConversations 结果缓存
type ConversationCache struct {
	// GetLastConversations 结果缓存 key: uid:tp:updatedAt:excludeChannelTypes:limit
	lastConversationsCache *lru.Cache[string, *LastConversationsResult]

	// 读写锁，保护缓存操作
	mu sync.RWMutex

	// 配置
	maxCacheSize int           // 缓存最大数量
	cacheTTL     time.Duration // 缓存过期时间

	wklog.Log
}

// LastConversationsResult GetLastConversations 的缓存结果
type LastConversationsResult struct {
	Conversations []Conversation `json:"conversations"`
	CachedAt      time.Time      `json:"cached_at"`
	TTL           time.Duration  `json:"ttl"`
}

// IsExpired 检查缓存是否过期
func (r *LastConversationsResult) IsExpired() bool {
	return time.Since(r.CachedAt) > r.TTL
}

// NewConversationCache 创建会话缓存
func NewConversationCache(maxCacheSize int) *ConversationCache {
	if maxCacheSize <= 0 {
		maxCacheSize = 2000 // 默认缓存2000个查询结果
	}

	lastConversationsCache, _ := lru.New[string, *LastConversationsResult](maxCacheSize)

	return &ConversationCache{
		lastConversationsCache: lastConversationsCache,
		maxCacheSize:           maxCacheSize,
		cacheTTL:               10 * time.Minute, // 缓存2分钟
		Log:                    wklog.NewWKLog("ConversationCache"),
	}
}

// GetLastConversations 从缓存获取 GetLastConversations 结果
func (c *ConversationCache) GetLastConversations(uid string, tp ConversationType, updatedAt uint64, excludeChannelTypes []uint8, limit int) ([]Conversation, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := c.getLastConversationsKey(uid, tp, updatedAt, excludeChannelTypes, limit)
	if result, ok := c.lastConversationsCache.Get(key); ok {
		if !result.IsExpired() {
			return result.Conversations, true
		}
		// 缓存过期，异步删除
		go func() {
			c.mu.Lock()
			c.lastConversationsCache.Remove(key)
			c.mu.Unlock()
		}()
	}
	return nil, false
}

// SetLastConversations 设置 GetLastConversations 结果到缓存
func (c *ConversationCache) SetLastConversations(uid string, tp ConversationType, updatedAt uint64, excludeChannelTypes []uint8, limit int, conversations []Conversation) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := c.getLastConversationsKey(uid, tp, updatedAt, excludeChannelTypes, limit)

	// 创建副本避免外部修改影响缓存
	conversationsCopy := make([]Conversation, len(conversations))
	copy(conversationsCopy, conversations)

	result := &LastConversationsResult{
		Conversations: conversationsCopy,
		CachedAt:      time.Now(),
		TTL:           c.cacheTTL,
	}
	c.lastConversationsCache.Add(key, result)
}

// InvalidateUserConversations 使指定用户的所有会话缓存失效
func (c *ConversationCache) InvalidateUserConversations(uid string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 删除该用户相关的所有缓存
	keys := c.lastConversationsCache.Keys()
	for _, key := range keys {
		if strings.HasPrefix(key, uid+":") {
			c.lastConversationsCache.Remove(key)
		}
	}
}

// UpdateConversationsInCache 智能更新缓存中的会话数据
func (c *ConversationCache) UpdateConversationsInCache(conversations []Conversation) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 按用户分组
	userConversations := make(map[string][]Conversation)
	for _, conv := range conversations {
		userConversations[conv.Uid] = append(userConversations[conv.Uid], conv)
	}

	// 为每个用户更新缓存
	for uid, convs := range userConversations {
		c.updateUserConversationsInCache(uid, convs)
	}
}

// updateUserConversationsInCache 更新指定用户的缓存
func (c *ConversationCache) updateUserConversationsInCache(uid string, updatedConversations []Conversation) {
	// 获取该用户相关的所有缓存键
	keys := c.lastConversationsCache.Keys()
	userKeys := make([]string, 0)
	for _, key := range keys {
		if strings.HasPrefix(key, uid+":") {
			userKeys = append(userKeys, key)
		}
	}

	// 为每个缓存项更新数据
	for _, key := range userKeys {
		if result, ok := c.lastConversationsCache.Get(key); ok && !result.IsExpired() {
			// 创建会话 channelId+channelType 到新会话的映射
			updatedMap := make(map[string]Conversation)
			for _, conv := range updatedConversations {
				channelKey := c.getChannelKey(conv.ChannelId, conv.ChannelType)
				updatedMap[channelKey] = conv
			}

			// 更新缓存中的会话数据
			updated := false
			newConversations := make([]Conversation, 0, len(result.Conversations))

			// 首先处理已存在的会话
			for _, cachedConv := range result.Conversations {
				channelKey := c.getChannelKey(cachedConv.ChannelId, cachedConv.ChannelType)
				if updatedConv, exists := updatedMap[channelKey]; exists {
					// 找到匹配的会话，使用新数据
					newConversations = append(newConversations, updatedConv)
					updated = true
					delete(updatedMap, channelKey) // 从映射中移除已处理的
				} else {
					// 保持原有数据
					newConversations = append(newConversations, cachedConv)
				}
			}

			// 检查是否有新增的会话（在updatedMap中剩余的）
			// 将新增的会话添加到缓存中
			for _, newConv := range updatedMap {
				newConversations = append(newConversations, newConv)
				updated = true
			}

			// 如果有更新，重新缓存
			if updated {
				// 按照更新时间重新排序（保持与 GetLastConversations 一致的排序）
				sort.Slice(newConversations, func(i, j int) bool {
					c1 := newConversations[i]
					c2 := newConversations[j]
					if c1.UpdatedAt == nil {
						return false
					}
					if c2.UpdatedAt == nil {
						return true
					}
					return c1.UpdatedAt.After(*c2.UpdatedAt)
				})

				newResult := &LastConversationsResult{
					Conversations: newConversations,
					CachedAt:      time.Now(),
					TTL:           c.cacheTTL,
				}
				c.lastConversationsCache.Add(key, newResult)
			}
		}
	}
}

// getChannelKey 生成频道唯一键
func (c *ConversationCache) getChannelKey(channelId string, channelType uint8) string {
	return fmt.Sprintf("%s:%d", channelId, channelType)
}

// GetCacheStats 获取缓存统计信息
func (c *ConversationCache) GetCacheStats() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return map[string]interface{}{
		"last_conversations_cache_len": c.lastConversationsCache.Len(),
		"last_conversations_cache_max": c.maxCacheSize,
		"cache_ttl_seconds":            c.cacheTTL.Seconds(),
	}
}

// ClearCache 清空所有缓存
func (c *ConversationCache) ClearCache() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lastConversationsCache.Purge()
	c.Info("Conversation cache cleared")
}

// 生成 GetLastConversations 缓存键
func (c *ConversationCache) getLastConversationsKey(uid string, tp ConversationType, updatedAt uint64, excludeChannelTypes []uint8, limit int) string {
	// 构建基础键
	keyParts := []string{
		uid,
		strconv.Itoa(int(tp)),
		strconv.FormatUint(updatedAt, 10),
		strconv.Itoa(limit),
	}

	// 处理 excludeChannelTypes
	if len(excludeChannelTypes) > 0 {
		// 排序确保一致性
		excludeTypes := make([]uint8, len(excludeChannelTypes))
		copy(excludeTypes, excludeChannelTypes)
		sort.Slice(excludeTypes, func(i, j int) bool {
			return excludeTypes[i] < excludeTypes[j]
		})

		excludeStrs := make([]string, len(excludeTypes))
		for i, t := range excludeTypes {
			excludeStrs[i] = strconv.Itoa(int(t))
		}
		keyParts = append(keyParts, strings.Join(excludeStrs, ","))
	} else {
		keyParts = append(keyParts, "")
	}

	key := strings.Join(keyParts, ":")

	// 如果键太长，使用 MD5 哈希
	if len(key) > 200 {
		hash := md5.Sum([]byte(key))
		return fmt.Sprintf("%s:%x", uid, hash)
	}

	return key
}

// GetCacheSize 获取当前缓存大小
func (c *ConversationCache) GetCacheSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastConversationsCache.Len()
}

// GetMaxCacheSize 获取最大缓存大小
func (c *ConversationCache) GetMaxCacheSize() int {
	return c.maxCacheSize
}

// SetCacheTTL 设置缓存过期时间
func (c *ConversationCache) SetCacheTTL(ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cacheTTL = ttl
}

// GetCacheTTL 获取缓存过期时间
func (c *ConversationCache) GetCacheTTL() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cacheTTL
}

// RemoveExpiredItems 清理过期的缓存项
func (c *ConversationCache) RemoveExpiredItems() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	keys := c.lastConversationsCache.Keys()
	removedCount := 0

	for _, key := range keys {
		if item, ok := c.lastConversationsCache.Get(key); ok && item.IsExpired() {
			c.lastConversationsCache.Remove(key)
			removedCount++
		}
	}

	if removedCount > 0 {
		c.Debug("Removed expired conversation cache items", zap.Int("count", removedCount))
	}

	return removedCount
}
