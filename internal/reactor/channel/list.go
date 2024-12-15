package reactor

import "sync"

type node struct {
	key     string
	channel *Channel
	next    *node
	pre     *node
}

func newChannelNode(key string, channel *Channel) *node {
	return &node{
		key:     key,
		channel: channel,
	}
}

type list struct {
	head  *node
	tail  *node
	count int

	mu sync.RWMutex
}

func newList() *list {
	return &list{}
}

func (l *list) add(ch *Channel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	node := newChannelNode(ch.key, ch)
	if l.head == nil {
		l.head = node
	} else {
		l.tail.next = node
		node.pre = l.tail
	}
	l.tail = node
	l.count++
}

func (l *list) remove(key string) *Channel {
	l.mu.Lock()
	defer l.mu.Unlock()
	node := l.head
	for node != nil {
		if node.key == key {
			if node.pre == nil {
				l.head = node.next
			} else {
				node.pre.next = node.next
			}
			if node.next == nil {
				l.tail = node.pre
			} else {
				node.next.pre = node.pre
			}
			l.count--
			return node.channel
		}
		node = node.next
	}
	return nil
}

func (l *list) get(key string) *Channel {
	l.mu.RLock()
	defer l.mu.RUnlock()
	node := l.head
	for node != nil {
		if node.key == key {
			return node.channel
		}
		node = node.next
	}
	return nil
}

func (l *list) exist(key string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	node := l.head
	for node != nil {
		if node.key == key {
			return true
		}
		node = node.next
	}
	return false
}

func (l *list) read(channels *[]*Channel) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	node := l.head
	for node != nil {
		*channels = append(*channels, node.channel)
		node = node.next
	}
}
