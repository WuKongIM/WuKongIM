package conversationactive

import (
	"sync"
)

// DirtyIndex 按 HashSlot 索引脏数据，支持 O(1) 访问
type DirtyIndex struct {
	mu      sync.RWMutex
	bySlot  map[uint16]map[cacheAddress]flushEntry
	counts  map[uint16]int
}

func newDirtyIndex() *DirtyIndex {
	return &DirtyIndex{
		bySlot: make(map[uint16]map[cacheAddress]flushEntry),
		counts: make(map[uint16]int),
	}
}

// add 添加一个脏数据条目到指定 hashSlot
func (d *DirtyIndex) add(hashSlot uint16, addr cacheAddress, entry flushEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()

	entries := d.bySlot[hashSlot]
	if entries == nil {
		entries = make(map[cacheAddress]flushEntry)
		d.bySlot[hashSlot] = entries
	}

	entries[addr] = entry
	d.counts[hashSlot] = len(entries)
}

// remove 从指定 hashSlot 删除一个脏数据条目
func (d *DirtyIndex) remove(hashSlot uint16, addr cacheAddress) {
	d.mu.Lock()
	defer d.mu.Unlock()

	entries := d.bySlot[hashSlot]
	if entries == nil {
		return
	}

	delete(entries, addr)
	if len(entries) == 0 {
		delete(d.bySlot, hashSlot)
		delete(d.counts, hashSlot)
	} else {
		d.counts[hashSlot] = len(entries)
	}
}

// count 返回指定 hashSlot 的脏数据数量
func (d *DirtyIndex) count(hashSlot uint16) int {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.counts[hashSlot]
}

// popN 从指定 hashSlot 弹出最多 n 个脏数据条目
// 如果 limit <= 0，返回该 slot 的所有条目
func (d *DirtyIndex) popN(hashSlot uint16, limit int) []flushEntry {
	d.mu.Lock()
	defer d.mu.Unlock()

	entries := d.bySlot[hashSlot]
	if entries == nil {
		return nil
	}

	var result []flushEntry
	if limit <= 0 {
		limit = len(entries)
	}

	// 收集条目
	for addr, entry := range entries {
		result = append(result, entry)
		delete(entries, addr)

		if len(result) >= limit {
			break
		}
	}

	// 更新计数
	if len(entries) == 0 {
		delete(d.bySlot, hashSlot)
		delete(d.counts, hashSlot)
	} else {
		d.counts[hashSlot] = len(entries)
	}

	return result
}

// totalCount 返回所有 hashSlot 的脏数据总数
func (d *DirtyIndex) totalCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()

	total := 0
	for _, count := range d.counts {
		total += count
	}
	return total
}
