package cluster

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
)

type slotQueue struct {
	head *slot
	tail *slot

	mu sync.Mutex
}

func newSlotQueue() *slotQueue {
	return &slotQueue{}
}

func (c *slotQueue) add(st *slot) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.head == nil {
		c.head = st
	} else {
		c.tail.next = st
		st.prev = c.tail
	}
	c.tail = st
}

func (c *slotQueue) remove(st *slot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st.prev == nil {
		c.head = st.next
	} else {
		st.prev.next = st.next
	}
	if st.next == nil {
		c.tail = st.prev
	} else {
		st.next.prev = st.prev
	}
	st.prev = nil
	st.next = nil
}

// func (c *channelQueue) pop() *Channel {
// 	channel := c.head
// 	if channel == nil {
// 		return nil
// 	}
// 	c.head = channel.next
// 	if c.head == nil {
// 		c.tail = nil
// 	} else {
// 		c.head.prev = nil
// 	}
// 	channel.prev = nil
// 	channel.next = nil
// 	return channel
// }

func (c *slotQueue) exist(slotId uint32) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for st := c.head; st != nil; st = st.next {
		if st.slotId == slotId {
			return true
		}
	}
	return false
}

func (c *slotQueue) get(slotId uint32) *slot {
	c.mu.Lock()
	defer c.mu.Unlock()
	for st := c.head; st != nil; st = st.next {
		if st.slotId == slotId {
			return st
		}
	}
	return nil
}

// 遍历频道
func (c *slotQueue) foreach(f func(st *slot)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for st := c.head; st != nil; st = st.next {
		f(st)
	}
}

// func (c *readyChannelQueue) remove(rd channelReady) {
// 	for e := c.queue.Front(); e != nil; e = e.Next() {
// 		if e.Value.(channelReady).channel == rd.channel {
// 			c.queue.Remove(e)
// 			return
// 		}
// 	}
// }

type slotReady struct {
	slot *slot
	replica.Ready
}

var emptySlotReady = slotReady{}
