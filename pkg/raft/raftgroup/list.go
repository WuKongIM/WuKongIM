package raftgroup

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

// raftNode 链表节点
type raftNode struct {
	key  string
	raft IRaft
	next *raftNode
}

type linkedList struct {
	head *raftNode
	tail *raftNode
	mu   sync.RWMutex // 保护链表的并发访问
	wklog.Log
}

// newLinkedList 创建新的链表
func newLinkedList() *linkedList {
	return &linkedList{
		Log: wklog.NewWKLog("raftGroup.linkedList"),
	}
}

// push 添加事件到链表尾部
func (ll *linkedList) push(raft IRaft) {
	ll.mu.Lock()
	defer ll.mu.Unlock()

	if ll.existNoLock(raft.Key()) {
		ll.Foucs("push: raft exist", zap.String("key", raft.Key()))
		return
	}

	node := &raftNode{key: raft.Key(), raft: raft}
	if ll.tail != nil {
		ll.tail.next = node
	} else {
		ll.head = node
	}
	ll.tail = node
}

func (ll *linkedList) existNoLock(key string) bool {
	for node := ll.head; node != nil; node = node.next {
		if node.key == key {
			return true
		}
	}
	return false
}

// remove 从链表中移除事件
func (ll *linkedList) remove(key string) {
	ll.mu.Lock()
	defer ll.mu.Unlock()

	var prev *raftNode
	for node := ll.head; node != nil; node = node.next {
		if node.raft.Key() == key {
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

func (ll *linkedList) get(key string) IRaft {
	ll.mu.Lock()
	defer ll.mu.Unlock()

	for node := ll.head; node != nil; node = node.next {
		if node.key == key {
			return node.raft
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
func (h *linkedList) readHandlers(rafts *[]IRaft) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	node := h.head
	for node != nil {
		*rafts = append(*rafts, node.raft)
		node = node.next
	}
}

func (h *linkedList) all() []IRaft {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var rafts []IRaft
	node := h.head
	for node != nil {
		rafts = append(rafts, node.raft)
		node = node.next
	}
	return rafts
}
