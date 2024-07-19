package reactor

import (
	"sync"
)

type handlerNode struct {
	key     string
	handler *handler
	next    *handlerNode
	pre     *handlerNode
}

func newHandlerNode(key string, handler *handler) *handlerNode {
	return &handlerNode{
		key:     key,
		handler: handler,
	}
}

type handlerList struct {
	head  *handlerNode
	tail  *handlerNode
	count int

	mu sync.RWMutex
}

func newHandlerList() *handlerList {
	return &handlerList{}
}

func (h *handlerList) add(handler *handler) {
	h.mu.Lock()
	defer h.mu.Unlock()
	node := newHandlerNode(handler.key, handler)
	if h.head == nil {
		h.head = node
	} else {
		h.tail.next = node
		node.pre = h.tail
	}
	h.tail = node
	h.count++
}

func (h *handlerList) remove(key string) *handler {
	h.mu.Lock()
	defer h.mu.Unlock()
	node := h.head
	for node != nil {
		if node.key == key {
			if node.pre == nil {
				h.head = node.next
			} else {
				node.pre.next = node.next
			}
			if node.next == nil {
				h.tail = node.pre
			} else {
				node.next.pre = node.pre
			}
			h.count--
			return node.handler
		}
		node = node.next
	}
	return nil
}

func (h *handlerList) get(key string) *handler {
	h.mu.RLock()
	defer h.mu.RUnlock()
	node := h.head
	for node != nil {
		if node.key == key {
			return node.handler
		}
		node = node.next
	}
	return nil
}

func (h *handlerList) exist(key string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	node := h.head
	for node != nil {
		if node.key == key {
			return true
		}
		node = node.next
	}
	return false
}

func (h *handlerList) readHandlers(handlers *[]*handler) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	node := h.head
	for node != nil {
		*handlers = append(*handlers, node.handler)
		node = node.next
	}
}

func (h *handlerList) iterator(f func(h *handler) bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	node := h.head
	for node != nil {
		if !f(node.handler) {
			break
		}
		node = node.next
	}

}

func (h *handlerList) len() int {
	return h.count
}
