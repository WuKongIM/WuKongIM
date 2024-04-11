package cluster

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
)

type channelQueue struct {
	head *channel
	tail *channel

	mu sync.RWMutex
}

func newChannelQueue() *channelQueue {
	return &channelQueue{}
}

func (c *channelQueue) add(channel *channel) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.head == nil {
		c.head = channel
	} else {
		c.tail.next = channel
		channel.prev = c.tail
	}
	c.tail = channel
}

func (c *channelQueue) remove(channel *channel) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.mu.RLock()
	defer c.mu.RUnlock()
	for channel := c.head; channel != nil; channel = channel.next {
		if channel.channelID == channelID && channel.channelType == channelType {
			return true
		}
	}
	return false
}

func (c *channelQueue) get(channelID string, channelType uint8) *channel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for channel := c.head; channel != nil; channel = channel.next {
		if channel.channelID == channelID && channel.channelType == channelType {
			return channel
		}
	}
	return nil
}

// 遍历频道
func (c *channelQueue) foreach(f func(channel *channel)) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for channel := c.head; channel != nil; channel = channel.next {
		f(channel)
	}
}

func (c *channelQueue) len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var n int
	for channel := c.head; channel != nil; channel = channel.next {
		n++
	}
	return n
}

type channelReady struct {
	channel *channel
	replica.Ready
}
