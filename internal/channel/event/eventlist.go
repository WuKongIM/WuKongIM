package event

import "sync"

// channelNode 链表节点
type channelNode struct {
	channelId   string
	channelType uint8
	handler     *channelHandler
	next        *channelNode
}

type linkedList struct {
	head *channelNode
	tail *channelNode
	mu   sync.RWMutex // 保护链表的并发访问
}

// newLinkedList 创建新的链表
func newLinkedList() *linkedList {
	return &linkedList{}
}

// push 添加事件到链表尾部
func (ll *linkedList) push(handler *channelHandler) {
	ll.mu.Lock()
	defer ll.mu.Unlock()
	node := &channelNode{channelId: handler.channelId, channelType: handler.channelType, handler: handler}
	if ll.tail != nil {
		ll.tail.next = node
	} else {
		ll.head = node
	}
	ll.tail = node
}

// remove 从链表中移除事件
func (ll *linkedList) remove(channelId string, channelType uint8) {
	ll.mu.Lock()
	defer ll.mu.Unlock()

	var prev *channelNode
	for node := ll.head; node != nil; node = node.next {
		if node.handler.channelId == channelId && node.handler.channelType == channelType {
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

func (ll *linkedList) get(channelId string, channelType uint8) *channelHandler {
	ll.mu.Lock()
	defer ll.mu.Unlock()

	for node := ll.head; node != nil; node = node.next {
		if node.channelId == channelId && node.channelType == channelType {
			return node.handler
		}
	}
	return nil
}

// func (ll *linkedList) len() int {
// 	ll.mu.Lock()
// 	defer ll.mu.Unlock()

//		var count int
//		for node := ll.head; node != nil; node = node.next {
//			count++
//		}
//		return count
//	}
func (h *linkedList) readHandlers(handlers *[]*channelHandler) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	node := h.head
	for node != nil {
		*handlers = append(*handlers, node.handler)
		node = node.next
	}
}
