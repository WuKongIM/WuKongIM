package event

import "sync"

// userNode 链表节点
type userNode struct {
	uid     string
	handler *userHandler
	next    *userNode
}

type linkedList struct {
	head *userNode
	tail *userNode
	mu   sync.RWMutex // 保护链表的并发访问
}

// newLinkedList 创建新的链表
func newLinkedList() *linkedList {
	return &linkedList{}
}

// push 添加事件到链表尾部
func (ll *linkedList) push(handler *userHandler) {
	ll.mu.Lock()
	defer ll.mu.Unlock()
	node := &userNode{uid: handler.Uid, handler: handler}
	if ll.tail != nil {
		ll.tail.next = node
	} else {
		ll.head = node
	}
	ll.tail = node
}

// remove 从链表中移除事件
func (ll *linkedList) remove(uid string) {
	ll.mu.Lock()
	defer ll.mu.Unlock()

	var prev *userNode
	for node := ll.head; node != nil; node = node.next {
		if node.handler.Uid == uid {
			// 删除节点
			if prev != nil {
				prev.next = node.next
			} else {
				ll.head = node.next
			}
			// 更新尾部指针
			if node.next == nil {
				ll.tail = prev
			}
			return
		}
		prev = node
	}
}

func (ll *linkedList) get(uid string) *userHandler {
	ll.mu.Lock()
	defer ll.mu.Unlock()

	for node := ll.head; node != nil; node = node.next {
		if node.uid == uid {
			return node.handler
		}
	}
	return nil
}

func (ll *linkedList) count() int {
	ll.mu.Lock()
	defer ll.mu.Unlock()

	var count int
	for node := ll.head; node != nil; node = node.next {
		count++
	}
	return count
}
func (h *linkedList) readHandlers(handlers *[]*userHandler) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	node := h.head
	for node != nil {
		*handlers = append(*handlers, node.handler)
		node = node.next
	}
}
