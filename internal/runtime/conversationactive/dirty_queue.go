package conversationactive

// dirtyQueueNode links one live dirty address inside a typed allocation-free queue.
type dirtyQueueNode struct {
	prev    cacheAddress
	next    cacheAddress
	hasPrev bool
	hasNext bool
}

// dirtyAddressQueue provides removable FIFO rotation without one heap allocation per dirty cycle.
// Callers must hold Manager.mu while using it.
type dirtyAddressQueue struct {
	nodes   map[cacheAddress]dirtyQueueNode
	head    cacheAddress
	tail    cacheAddress
	hasHead bool
	hasTail bool
}

func (q *dirtyAddressQueue) Add(address cacheAddress) {
	if q.nodes == nil {
		q.nodes = make(map[cacheAddress]dirtyQueueNode)
	}
	if _, ok := q.nodes[address]; ok {
		return
	}
	node := dirtyQueueNode{}
	if q.hasTail {
		node.prev = q.tail
		node.hasPrev = true
		tail := q.nodes[q.tail]
		tail.next = address
		tail.hasNext = true
		q.nodes[q.tail] = tail
	} else {
		q.head = address
		q.hasHead = true
	}
	q.nodes[address] = node
	q.tail = address
	q.hasTail = true
}

func (q *dirtyAddressQueue) Remove(address cacheAddress) {
	node, ok := q.nodes[address]
	if !ok {
		return
	}
	if node.hasPrev {
		prev := q.nodes[node.prev]
		prev.next = node.next
		prev.hasNext = node.hasNext
		q.nodes[node.prev] = prev
	} else {
		q.head = node.next
		q.hasHead = node.hasNext
	}
	if node.hasNext {
		next := q.nodes[node.next]
		next.prev = node.prev
		next.hasPrev = node.hasPrev
		q.nodes[node.next] = next
	} else {
		q.tail = node.prev
		q.hasTail = node.hasPrev
	}
	delete(q.nodes, address)
	if len(q.nodes) == 0 {
		q.head = cacheAddress{}
		q.tail = cacheAddress{}
		q.hasHead = false
		q.hasTail = false
	}
}

func (q *dirtyAddressQueue) MoveToBack(address cacheAddress) {
	if !q.hasTail || q.tail == address {
		return
	}
	if _, ok := q.nodes[address]; !ok {
		return
	}
	q.Remove(address)
	q.Add(address)
}

func (q *dirtyAddressQueue) Front() (cacheAddress, bool) {
	return q.head, q.hasHead
}

func (q *dirtyAddressQueue) Next(address cacheAddress) (cacheAddress, bool) {
	node, ok := q.nodes[address]
	if !ok || !node.hasNext {
		return cacheAddress{}, false
	}
	return node.next, true
}

func (q *dirtyAddressQueue) Len() int {
	return len(q.nodes)
}
