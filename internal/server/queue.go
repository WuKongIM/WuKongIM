package server

import (
	"sync"

	"github.com/sasha-s/go-deadlock"
)

type channelNode struct {
	key  string
	ch   *channel
	next *channelNode
	pre  *channelNode
}

func newChannelNode(key string, ch *channel) *channelNode {
	return &channelNode{
		key: key,
		ch:  ch,
	}
}

type channelList struct {
	head  *channelNode
	tail  *channelNode
	count int

	mu deadlock.RWMutex
}

func newChannelList() *channelList {
	return &channelList{}
}

func (c *channelList) add(ch *channel) {
	c.mu.Lock()
	defer c.mu.Unlock()
	node := newChannelNode(ch.key, ch)
	if c.head == nil {
		c.head = node
	} else {
		c.tail.next = node
		node.pre = c.tail
	}
	c.tail = node
	c.count++
}

func (c *channelList) remove(key string) *channel {
	c.mu.Lock()
	defer c.mu.Unlock()
	node := c.head
	for node != nil {
		if node.key == key {
			if node.pre == nil {
				c.head = node.next
			} else {
				node.pre.next = node.next
			}
			if node.next == nil {
				c.tail = node.pre
			} else {
				node.next.pre = node.pre
			}
			c.count--
			return node.ch
		}
		node = node.next
	}
	return nil
}

func (c *channelList) get(key string) *channel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	node := c.head
	for node != nil {
		if node.key == key {
			return node.ch
		}
		node = node.next
	}
	return nil
}

// func (c *channelList) exist(key string) bool {
// 	c.mu.RLock()
// 	defer c.mu.RUnlock()
// 	node := c.head
// 	for node != nil {
// 		if node.key == key {
// 			return true
// 		}
// 		node = node.next
// 	}
// 	return false
// }

func (c *channelList) iter(f func(ch *channel)) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	node := c.head
	for node != nil {
		f(node.ch)
		node = node.next
	}
}

// func (c *channelList) len() int {
// 	c.mu.RLock()
// 	defer c.mu.RUnlock()
// 	return c.count
// }

type userNode struct {
	key  string
	user *userHandler
	next *userNode
	pre  *userNode
}

func newUserNode(key string, user *userHandler) *userNode {
	return &userNode{
		key:  key,
		user: user,
	}
}

type userHandlerList struct {
	head  *userNode
	tail  *userNode
	count int

	mu sync.RWMutex
}

func newUserHandlerList() *userHandlerList {
	return &userHandlerList{}
}

func (c *userHandlerList) add(u *userHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	node := newUserNode(u.uid, u)
	if c.head == nil {
		c.head = node
	} else {
		c.tail.next = node
		node.pre = c.tail
	}
	c.tail = node
	c.count++
}

func (c *userHandlerList) remove(key string) *userHandler {
	c.mu.Lock()
	defer c.mu.Unlock()
	node := c.head
	for node != nil {
		if node.key == key {
			if node.pre == nil {
				c.head = node.next
			} else {
				node.pre.next = node.next
			}
			if node.next == nil {
				c.tail = node.pre
			} else {
				node.next.pre = node.pre
			}
			c.count--
			return node.user
		}
		node = node.next
	}
	return nil
}

func (c *userHandlerList) get(key string) *userHandler {
	c.mu.RLock()
	defer c.mu.RUnlock()
	node := c.head
	for node != nil {
		if node.key == key {
			return node.user
		}
		node = node.next
	}
	return nil
}

// func (c *userList) exist(key string) bool {
// 	c.mu.RLock()
// 	defer c.mu.RUnlock()
// 	node := c.head
// 	for node != nil {
// 		if node.key == key {
// 			return true
// 		}
// 		node = node.next
// 	}
// 	return false
// }

func (c *userHandlerList) iter(f func(ch *userHandler) bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	node := c.head
	for node != nil {
		cn := f(node.user)
		if !cn {
			break
		}
		node = node.next
	}
}

func (c *userHandlerList) len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.count
}

type streamNode struct {
	key  string
	sm   *stream
	next *streamNode
	pre  *streamNode
}

type streamList struct {
	head  *streamNode
	tail  *streamNode
	count int
	mu    sync.RWMutex
}

func newStreamList() *streamList {
	return &streamList{}
}

func (c *streamList) add(sm *stream) {
	c.mu.Lock()
	defer c.mu.Unlock()
	node := &streamNode{
		key: sm.streamNo,
		sm:  sm,
	}
	if c.head == nil {
		c.head = node
	} else {
		c.tail.next = node
		node.pre = c.tail
	}
	c.tail = node
	c.count++
}

func (c *streamList) remove(key string) *stream {
	c.mu.Lock()
	defer c.mu.Unlock()
	node := c.head
	for node != nil {
		if node.key == key {
			if node.pre == nil {
				c.head = node.next
			} else {
				node.pre.next = node.next
			}
			if node.next == nil {
				c.tail = node.pre
			} else {
				node.next.pre = node.pre
			}
			c.count--
			return node.sm
		}
		node = node.next
	}
	return nil
}

func (c *streamList) get(key string) *stream {
	c.mu.RLock()
	defer c.mu.RUnlock()
	node := c.head
	for node != nil {
		if node.key == key {
			return node.sm
		}
		node = node.next
	}
	return nil
}

func (c *streamList) readStreams(streams *[]*stream) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	node := c.head
	for node != nil {
		*streams = append(*streams, node.sm)
		node = node.next
	}
}

func (c *streamList) hasPayloadUnDecrypt() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	node := c.head
	for node != nil {
		if node.sm.hasPayloadUnDecrypt() {
			return true
		}
		node = node.next
	}
	return false
}

func (c *streamList) hasUnDeliver() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	node := c.head
	for node != nil {
		if node.sm.hasUnDeliver() {
			return true
		}
		node = node.next
	}
	return false
}

func (c *streamList) hasUnforward() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	node := c.head
	for node != nil {
		if node.sm.hasUnforward() {
			return true
		}
		node = node.next
	}
	return false
}

func (c *streamList) payloadUnDecryptMessages() []ReactorChannelMessage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var msgs []ReactorChannelMessage
	node := c.head
	for node != nil {
		if node.sm.hasPayloadUnDecrypt() {
			msgs = append(msgs, node.sm.payloadUnDecryptMessages()...)
		}
		node = node.next
	}
	return msgs
}

func (c *streamList) unDeliverMessages() []ReactorChannelMessage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var msgs []ReactorChannelMessage
	node := c.head
	for node != nil {
		if node.sm.hasUnDeliver() {
			msgs = append(msgs, node.sm.unDeliverMessages()...)
		}
		node = node.next
	}
	return msgs
}

func (c *streamList) unforwardMessages() []ReactorChannelMessage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var msgs []ReactorChannelMessage
	node := c.head
	for node != nil {
		if node.sm.hasUnforward() {
			msgs = append(msgs, node.sm.unforwardMessages()...)
		}
		node = node.next
	}
	return msgs
}

func (s *streamList) tick() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	node := s.head
	for node != nil {
		node.sm.tick()
		node = node.next
	}
}
