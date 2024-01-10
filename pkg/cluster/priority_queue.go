package cluster

import (
	"container/heap"
	"sync"
)

// PriorityQueue is a priority queue implementation based
// container/heap
type PriorityQueue struct {
	sync.Mutex
	items     []ChannelEvent
	lookupMap map[string]ChannelEvent
}

// New creates a new priority queue, containing items of type T with
// priority V.
func NewPriorityQueue() *PriorityQueue {
	pq := &PriorityQueue{
		items:     make([]ChannelEvent, 0),
		lookupMap: make(map[string]ChannelEvent),
	}
	heap.Init(pq)

	return pq
}

// Len implements sort.Interface
func (pq *PriorityQueue) Len() int {
	return len(pq.items)
}

// Less implements sort.Interface
func (pq *PriorityQueue) Less(i, j int) bool {

	return pq.items[i].Priority > pq.items[j].Priority
}

// Swap implements sort.Interface
func (pq *PriorityQueue) Swap(i, j int) {
	pq.items[i], pq.items[j] = pq.items[j], pq.items[i]
	pq.items[i].index = i
	pq.items[j].index = j
}

// Push implements heap.Interface
func (pq *PriorityQueue) Push(x any) {
	n := len(pq.items)
	item := x.(ChannelEvent)
	item.index = n
	pq.items = append(pq.items, item)
}

// Pop implements heap.Interface
func (pq *PriorityQueue) Pop() any {
	old := pq.items
	n := len(old)
	item := old[n-1]
	item.index = -1 // for safety
	pq.items = old[0 : n-1]
	return item
}

// Put adds a value with the given priority to the priority queue
func (pq *PriorityQueue) Put(event ChannelEvent) {
	pq.Lock()
	defer pq.Unlock()

	pq.lookupMap[event.Channel.GetChannelKey()] = event

	heap.Push(pq, event)
}

// Get returns the next item from the priority queue
func (pq *PriorityQueue) Get() ChannelEvent {
	pq.Lock()
	defer pq.Unlock()
	item := heap.Pop(pq).(ChannelEvent)
	delete(pq.lookupMap, item.Channel.GetChannelKey())
	return item
}

// IsEmpty returns a boolean indicating whether the priority queue is
// empty or not
func (pq *PriorityQueue) IsEmpty() bool {
	pq.Lock()
	defer pq.Unlock()
	return pq.Len() == 0
}
