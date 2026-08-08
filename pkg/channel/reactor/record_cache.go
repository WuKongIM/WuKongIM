package reactor

import ch "github.com/WuKongIM/WuKongIM/pkg/channel"

// recentRecordCache keeps a contiguous, leader-owned suffix of recently appended records.
type recentRecordCache struct {
	// baseOffset is the first cached record index; zero means the cache is empty.
	baseOffset uint64
	// records holds a contiguous suffix ordered by increasing Index.
	records []ch.Record
	// bytes is the effective payload byte size of records.
	bytes int
	// maxRecords bounds the number of retained records; non-positive disables the cache.
	maxRecords int
	// maxBytes bounds retained payload bytes while preserving at least the newest record.
	maxBytes int
}

func newRecentRecordCache(maxRecords int, maxBytes int) recentRecordCache {
	return recentRecordCache{maxRecords: maxRecords, maxBytes: maxBytes}
}

func (c *recentRecordCache) enabled() bool {
	return c.maxRecords > 0
}

func (c *recentRecordCache) empty() bool {
	return len(c.records) == 0
}

func (c *recentRecordCache) reset() {
	c.baseOffset = 0
	c.records = nil
	c.bytes = 0
}

func (c *recentRecordCache) append(records []ch.Record) {
	if !c.enabled() {
		c.reset()
		return
	}
	if len(records) == 0 {
		return
	}
	suffix := continuousIndexedSuffix(records)
	if len(suffix) == 0 {
		c.reset()
		return
	}
	cloned := cloneCacheRecords(suffix)
	if c.empty() || cloned[0].Index != c.lastOffset()+1 {
		c.records = cloned
	} else {
		c.records = append(c.records, cloned...)
	}
	c.trim()
}

func (c *recentRecordCache) lastOffset() uint64 {
	if c.empty() {
		return 0
	}
	return c.records[len(c.records)-1].Index
}

func (c *recentRecordCache) base() uint64 {
	return c.baseOffset
}

func (c *recentRecordCache) hasSuffixAfter(offset uint64) bool {
	return !c.empty() && offset < c.baseOffset
}

func (c *recentRecordCache) covers(offset uint64) bool {
	return !c.empty() && offset >= c.baseOffset && offset <= c.lastOffset()
}

func (c *recentRecordCache) slice(from uint64, maxOffset uint64, maxBytes int) ([]ch.Record, bool) {
	if !c.covers(from) {
		return nil, false
	}
	last := c.lastOffset()
	if maxOffset == 0 || maxOffset > last {
		maxOffset = last
	}
	if maxOffset < from {
		return []ch.Record{}, true
	}
	start := int(from - c.baseOffset)
	out := make([]ch.Record, 0, int(maxOffset-from)+1)
	used := 0
	for i := start; i < len(c.records); i++ {
		record := c.records[i]
		if record.Index > maxOffset {
			break
		}
		size := cacheRecordSize(record)
		if maxBytes > 0 && used+size > maxBytes && len(out) > 0 {
			break
		}
		used += size
		out = append(out, cloneCacheRecord(record))
	}
	return out, true
}

// discardThrough releases the cached prefix at or below the supplied durable
// offset. Cache misses remain safe because follower pulls fall back to the
// channel store.
func (c *recentRecordCache) discardThrough(offset uint64) {
	if c.empty() || offset < c.baseOffset {
		return
	}
	if offset >= c.lastOffset() {
		c.reset()
		return
	}
	count := int(offset-c.baseOffset) + 1
	removed := cacheRecordsBytes(c.records[:count])
	clear(c.records[:count])
	c.records = c.records[count:]
	c.bytes -= removed
	if c.bytes < 0 {
		c.bytes = 0
	}
	c.baseOffset = c.records[0].Index
}

func (c *recentRecordCache) trim() {
	if !c.enabled() {
		c.reset()
		return
	}
	if len(c.records) > c.maxRecords {
		drop := len(c.records) - c.maxRecords
		clear(c.records[:drop])
		c.records = c.records[drop:]
	}
	c.bytes = cacheRecordsBytes(c.records)
	if c.maxBytes > 0 {
		for len(c.records) > 1 && c.bytes > c.maxBytes {
			c.bytes -= cacheRecordSize(c.records[0])
			c.records[0] = ch.Record{}
			c.records = c.records[1:]
		}
	}
	if len(c.records) == 0 {
		c.reset()
		return
	}
	c.baseOffset = c.records[0].Index
}

func continuousIndexedSuffix(records []ch.Record) []ch.Record {
	if len(records) == 0 || records[len(records)-1].Index == 0 {
		return nil
	}
	end := len(records)
	start := end - 1
	for start > 0 {
		prev := records[start-1].Index
		cur := records[start].Index
		if prev == 0 || prev+1 != cur {
			break
		}
		start--
	}
	return records[start:end]
}

func cloneCacheRecords(records []ch.Record) []ch.Record {
	out := make([]ch.Record, len(records))
	for i, record := range records {
		out[i] = cloneCacheRecord(record)
	}
	return out
}

func cloneCacheRecord(record ch.Record) ch.Record {
	if len(record.Payload) > 0 {
		record.Payload = append([]byte(nil), record.Payload...)
	}
	return record
}

func cacheRecordsBytes(records []ch.Record) int {
	total := 0
	for _, record := range records {
		total += cacheRecordSize(record)
	}
	return total
}

func cacheRecordSize(record ch.Record) int {
	if record.SizeBytes > 0 {
		return record.SizeBytes
	}
	return len(record.Payload)
}

// releaseRecentRecordsAcknowledgedByAllFollowers keeps only records that at
// least one configured follower has not durably acknowledged. The cache is an
// optimization; a follower that later requests an older offset reads it from
// the durable store.
func (r *Reactor) releaseRecentRecordsAcknowledgedByAllFollowers(rc *runtimeChannel) {
	if rc == nil || rc.state == nil || rc.state.Role != ch.RoleLeader || rc.recentRecords.empty() {
		return
	}
	acknowledgedThrough := rc.state.LEO
	for _, replica := range rc.state.Replicas {
		if replica == r.cfg.LocalNode {
			continue
		}
		progress := rc.state.Progress[replica]
		if progress.Match < acknowledgedThrough {
			acknowledgedThrough = progress.Match
		}
	}
	rc.recentRecords.discardThrough(acknowledgedThrough)
}
