package sequence

import (
	"sync"
	"sync/atomic"
)

type Allocator interface {
	NextMessageID() int64
	NextChannelSequence(channelKey string) uint32
}

type MemoryAllocator struct {
	nextMessageID atomic.Int64
	mu            sync.Mutex
	channelSeq    map[string]uint32
}

func (a *MemoryAllocator) NextMessageID() int64 {
	if a == nil {
		return 0
	}
	return a.nextMessageID.Add(1)
}

func (a *MemoryAllocator) NextChannelSequence(channelKey string) uint32 {
	if a == nil {
		return 0
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.channelSeq == nil {
		a.channelSeq = make(map[string]uint32)
	}

	next := a.channelSeq[channelKey] + 1
	a.channelSeq[channelKey] = next
	return next
}
