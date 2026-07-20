package diagnostics

import "fmt"

type indexes struct {
	trace       *boundedIndex
	client      *boundedIndex
	channel     *boundedIndex
	uid         *boundedIndex
	channelSeq  *boundedIndex
	node        *boundedIndex
	peerNode    *boundedIndex
	slot        *boundedIndex
	stage       *boundedIndex
	channelSpan *boundedRangeIndex
}

type rangeEntry struct {
	start uint64
	end   uint64
	id    uint64
}

func newIndexes(maxEventsPerKey, maxKeys int) *indexes {
	return &indexes{
		trace:       newBoundedIndex(maxEventsPerKey, maxKeys),
		client:      newBoundedIndex(maxEventsPerKey, maxKeys),
		channel:     newBoundedIndex(maxEventsPerKey, maxKeys),
		uid:         newBoundedIndex(maxEventsPerKey, maxKeys),
		channelSeq:  newBoundedIndex(maxEventsPerKey, maxKeys),
		node:        newBoundedIndex(maxEventsPerKey, maxKeys),
		peerNode:    newBoundedIndex(maxEventsPerKey, maxKeys),
		slot:        newBoundedIndex(maxEventsPerKey, maxKeys),
		stage:       newBoundedIndex(maxEventsPerKey, maxKeys),
		channelSpan: newBoundedRangeIndex(maxEventsPerKey, maxKeys),
	}
}

func (i *indexes) add(id uint64, event Event) {
	if event.TraceID != "" {
		i.trace.Add(event.TraceID, id)
	}
	if event.ClientMsgNo != "" {
		i.client.Add(event.ClientMsgNo, id)
	}
	if event.ChannelKey != "" {
		i.channel.Add(event.ChannelKey, id)
		if event.MessageSeq > 0 {
			i.channelSeq.Add(channelSeqKey(event.ChannelKey, event.MessageSeq), id)
		}
	}
	if event.FromUID != "" {
		i.uid.Add(event.FromUID, id)
	}
	if event.NodeID > 0 {
		i.node.Add(fmt.Sprint(event.NodeID), id)
	}
	if event.PeerNodeID > 0 {
		i.peerNode.Add(fmt.Sprint(event.PeerNodeID), id)
	}
	if event.SlotID > 0 {
		i.slot.Add(fmt.Sprint(event.SlotID), id)
	}
	if event.Stage != "" {
		i.stage.Add(string(event.Stage), id)
	}
	if event.ChannelKey != "" && event.RangeStart > 0 && event.RangeEnd >= event.RangeStart {
		i.channelSpan.Add(event.ChannelKey, rangeEntry{start: event.RangeStart, end: event.RangeEnd, id: id})
	}
}

func (i *indexes) lookup(q Query) []uint64 {
	var ids []uint64
	if q.TraceID != "" {
		ids = append(ids, i.trace.Get(q.TraceID)...)
	}
	if q.ClientMsgNo != "" {
		ids = append(ids, i.client.Get(q.ClientMsgNo)...)
	}
	if q.ChannelKey != "" {
		if q.MessageSeq > 0 {
			ids = append(ids, i.channelSeq.Get(channelSeqKey(q.ChannelKey, q.MessageSeq))...)
			ids = append(ids, i.channelSpan.Get(q.ChannelKey, q.MessageSeq)...)
		} else {
			ids = append(ids, i.channel.Get(q.ChannelKey)...)
		}
	}
	if q.UID != "" {
		ids = append(ids, i.uid.Get(q.UID)...)
	}
	if q.SlotID > 0 {
		ids = append(ids, i.slot.Get(fmt.Sprint(q.SlotID))...)
	}
	if q.Stage != "" {
		ids = append(ids, i.stage.Get(string(q.Stage))...)
	}
	return uniqueIDs(ids)
}

type boundedIndex struct {
	maxEvents int
	maxKeys   int
	values    map[string][]uint64
	order     []string
}

func newBoundedIndex(maxEvents, maxKeys int) *boundedIndex {
	return &boundedIndex{maxEvents: maxEvents, maxKeys: maxKeys, values: make(map[string][]uint64)}
}

func (b *boundedIndex) Add(key string, id uint64) {
	if key == "" {
		return
	}
	if _, ok := b.values[key]; !ok {
		b.order = append(b.order, key)
	}
	ids := append(b.values[key], id)
	if len(ids) > b.maxEvents {
		ids = ids[len(ids)-b.maxEvents:]
	}
	b.values[key] = ids
	b.evictKeys()
}

func (b *boundedIndex) Get(key string) []uint64 {
	ids := b.values[key]
	if len(ids) == 0 {
		return nil
	}
	return append([]uint64(nil), ids...)
}

func (b *boundedIndex) Len() int { return len(b.values) }

func (b *boundedIndex) evictKeys() {
	for len(b.values) > b.maxKeys && len(b.order) > 0 {
		key := b.order[0]
		b.order = b.order[1:]
		delete(b.values, key)
	}
}

type boundedRangeIndex struct {
	maxEvents int
	maxKeys   int
	values    map[string][]rangeEntry
	order     []string
}

func newBoundedRangeIndex(maxEvents, maxKeys int) *boundedRangeIndex {
	return &boundedRangeIndex{maxEvents: maxEvents, maxKeys: maxKeys, values: make(map[string][]rangeEntry)}
}

func (b *boundedRangeIndex) Add(key string, entry rangeEntry) {
	if key == "" {
		return
	}
	if _, ok := b.values[key]; !ok {
		b.order = append(b.order, key)
	}
	entries := append(b.values[key], entry)
	if len(entries) > b.maxEvents {
		entries = entries[len(entries)-b.maxEvents:]
	}
	b.values[key] = entries
	b.evictKeys()
}

func (b *boundedRangeIndex) Get(key string, seq uint64) []uint64 {
	entries := b.values[key]
	if len(entries) == 0 {
		return nil
	}
	ids := make([]uint64, 0, len(entries))
	for _, entry := range entries {
		if seq >= entry.start && seq <= entry.end {
			ids = append(ids, entry.id)
		}
	}
	return ids
}

func (b *boundedRangeIndex) evictKeys() {
	for len(b.values) > b.maxKeys && len(b.order) > 0 {
		key := b.order[0]
		b.order = b.order[1:]
		delete(b.values, key)
	}
}

func channelSeqKey(channelKey string, seq uint64) string {
	return fmt.Sprintf("%s#%d", channelKey, seq)
}

func uniqueIDs(ids []uint64) []uint64 {
	if len(ids) < 2 {
		return ids
	}
	seen := make(map[uint64]struct{}, len(ids))
	out := ids[:0]
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
