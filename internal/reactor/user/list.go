package reactor

import "sync"

type node struct {
	key  string
	user *User
	next *node
	pre  *node
}

func newUserNode(key string, user *User) *node {
	return &node{
		key:  key,
		user: user,
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

func (l *list) add(u *User) {
	l.mu.Lock()
	defer l.mu.Unlock()
	node := newUserNode(u.uid, u)
	if l.head == nil {
		l.head = node
	} else {
		l.tail.next = node
		node.pre = l.tail
	}
	l.tail = node
	l.count++
}

func (l *list) remove(key string) *User {
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
			return node.user
		}
		node = node.next
	}
	return nil
}

func (l *list) get(key string) *User {
	l.mu.RLock()
	defer l.mu.RUnlock()
	node := l.head
	for node != nil {
		if node.key == key {
			return node.user
		}
		node = node.next
	}
	return nil
}

func (l *list) readHandlers(users *[]*User) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	node := l.head
	for node != nil {
		*users = append(*users, node.user)
		node = node.next
	}
}
