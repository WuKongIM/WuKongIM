package cluster

import (
	"container/list"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
)

type channelQueue struct {
	head *channel
	tail *channel
}

func newChannelQueue() *channelQueue {
	return &channelQueue{}
}

func (c *channelQueue) add(channel *channel) {
	if c.head == nil {
		c.head = channel
	} else {
		c.tail.next = channel
		channel.prev = c.tail
	}
	c.tail = channel
}

func (c *channelQueue) remove(channel *channel) {
	if channel.prev == nil {
		c.head = channel.next
	} else {
		channel.prev.next = channel.next
	}
	if channel.next == nil {
		c.tail = channel.prev
	} else {
		channel.next.prev = channel.prev
	}
	channel.prev = nil
	channel.next = nil
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

func (c *channelQueue) exist(channelID string, channelType uint8) bool {
	for channel := c.head; channel != nil; channel = channel.next {
		if channel.channelID == channelID && channel.channelType == channelType {
			return true
		}
	}
	return false
}

func (c *channelQueue) get(channelID string, channelType uint8) *channel {
	for channel := c.head; channel != nil; channel = channel.next {
		if channel.channelID == channelID && channel.channelType == channelType {
			return channel
		}
	}
	return nil
}

// 遍历频道
func (c *channelQueue) foreach(f func(channel *channel)) {
	for channel := c.head; channel != nil; channel = channel.next {
		f(channel)
	}
}

type readyChannelQueue struct {
	queue *list.List
}

func newReadyChannelQueue() *readyChannelQueue {
	return &readyChannelQueue{
		queue: list.New(),
	}
}

func (c *readyChannelQueue) add(rd channelReady) {
	c.queue.PushBack(rd)
}

func (c *readyChannelQueue) pop() channelReady {
	e := c.queue.Front()
	if e == nil || e.Value == nil {
		return channelReady{}
	}
	c.queue.Remove(e)
	return e.Value.(channelReady)
}

func (c readyChannelQueue) len() int {
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

type channelReady struct {
	channel *channel
	replica.Ready
}
