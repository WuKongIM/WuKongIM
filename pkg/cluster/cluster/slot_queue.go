package cluster

import (
	"container/list"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
)

type slotQueue struct {
	head *slot
	tail *slot
}

func newSlotQueue() *slotQueue {
	return &slotQueue{}
}

func (c *slotQueue) add(st *slot) {
	if c.head == nil {
		c.head = st
	} else {
		c.tail.next = st
		st.prev = c.tail
	}
	c.tail = st
}

func (c *slotQueue) remove(st *slot) {
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
	for st := c.head; st != nil; st = st.next {
		if st.slotId == slotId {
			return true
		}
	}
	return false
}

func (c *slotQueue) get(slotId uint32) *slot {
	for st := c.head; st != nil; st = st.next {
		if st.slotId == slotId {
			return st
		}
	}
	return nil
}

// 遍历频道
func (c *slotQueue) foreach(f func(st *slot)) {
	for st := c.head; st != nil; st = st.next {
		f(st)
	}
}

type readySlotQueue struct {
	queue *list.List
}

func newReadySlotQueue() *readySlotQueue {
	return &readySlotQueue{
		queue: list.New(),
	}
}

func (c *readySlotQueue) add(rd slotReady) {
	c.queue.PushBack(rd)
}

func (c *readySlotQueue) pop() slotReady {
	e := c.queue.Front()
	if e == nil || e.Value == nil {
		return slotReady{}
	}
	c.queue.Remove(e)
	return e.Value.(slotReady)
}

func (c readySlotQueue) len() int {
	return c.queue.Len()
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
