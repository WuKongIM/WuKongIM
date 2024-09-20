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

type userList struct {
	head  *userNode
	tail  *userNode
	count int

	mu sync.RWMutex
}

func newUserList() *userList {
	return &userList{}
}

func (c *userList) add(u *userHandler) {
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

func (c *userList) remove(key string) *userHandler {
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

func (c *userList) get(key string) *userHandler {
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

func (c *userList) iter(f func(ch *userHandler) bool) {
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

// func (c *userList) len() int {
// 	c.mu.RLock()
// 	defer c.mu.RUnlock()
// 	return c.count
// }
