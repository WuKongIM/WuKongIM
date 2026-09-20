package delivery

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
)

const noPlanQueueNode = -1

// orderedPlanQueue preserves one FIFO per exact Channel, with at most one
// executing plan per Channel. Idle workers take ready Channels in round-robin
// order, so an unrelated Channel never waits behind a hash collision.
type orderedPlanQueue struct {
	capacity int
	// nodes and slots bound queued ownership independently of active workers.
	nodes []orderedPlanNode
	slots chan struct{}
	ready chan struct{}

	// mu protects Channel ownership, the runnable FIFO, and free node links.
	mu sync.Mutex
	// channels retains only queued or executing Channels: at most capacity
	// plus the fixed worker count, independent of historical Channel count.
	channels             map[planChannelKey]*orderedPlanChannel
	readyHead, readyTail *orderedPlanChannel
	freeHead             int
	depth                atomic.Int64
}

type planChannelKey struct {
	id   string
	kind uint8
}

type orderedPlanChannel struct {
	key        planChannelKey
	head, tail int
	active     bool
	nextReady  *orderedPlanChannel
}

type orderedPlanNode struct {
	plan onlinedelivery.RecipientDeliveryPlan
	next int
}

func planChannel(plan onlinedelivery.RecipientDeliveryPlan) planChannelKey {
	return planChannelKey{id: plan.Event.ChannelID, kind: plan.Event.ChannelType}
}

// newOrderedPlanQueue preallocates every queued plan slot. Channel state is
// released as soon as its final executing plan completes.
func newOrderedPlanQueue(capacity int) *orderedPlanQueue {
	if capacity <= 0 {
		return nil
	}
	queue := &orderedPlanQueue{
		capacity: capacity,
		nodes:    make([]orderedPlanNode, capacity),
		slots:    make(chan struct{}, capacity),
		ready:    make(chan struct{}, 1),
		channels: make(map[planChannelKey]*orderedPlanChannel),
		freeHead: 0,
	}
	for index := range queue.nodes {
		queue.nodes[index].next = index + 1
		queue.slots <- struct{}{}
	}
	queue.nodes[len(queue.nodes)-1].next = noPlanQueueNode
	return queue
}

// enqueue transfers immutable plan ownership after acquiring global capacity.
func (q *orderedPlanQueue) enqueue(ctx context.Context, acceptDone <-chan struct{}, plan onlinedelivery.RecipientDeliveryPlan) error {
	if q == nil {
		return ErrRuntimeClosed
	}
	select {
	case <-q.slots:
	case <-acceptDone:
		return ErrRuntimeClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-acceptDone:
		q.slots <- struct{}{}
		return ErrRuntimeClosed
	default:
	}

	key := planChannel(plan)
	q.mu.Lock()
	nodeIndex := q.freeHead
	node := &q.nodes[nodeIndex]
	q.freeHead = node.next
	node.plan, node.next = plan, noPlanQueueNode
	channel := q.channels[key]
	if channel == nil {
		channel = &orderedPlanChannel{key: key, head: noPlanQueueNode, tail: noPlanQueueNode}
		q.channels[key] = channel
	}
	wasEmpty := channel.head == noPlanQueueNode
	if wasEmpty {
		channel.head = nodeIndex
	} else {
		q.nodes[channel.tail].next = nodeIndex
	}
	channel.tail = nodeIndex
	q.depth.Add(1)
	if wasEmpty && !channel.active {
		q.appendReady(channel)
	}
	q.mu.Unlock()
	return nil
}

// appendReady is called under mu only for a nonempty, inactive Channel that
// is not already runnable. Completing a page rotates its Channel to the tail.
func (q *orderedPlanQueue) appendReady(channel *orderedPlanChannel) {
	if q.readyTail == nil {
		q.readyHead = channel
	} else {
		q.readyTail.nextReady = channel
	}
	q.readyTail = channel
	q.signalReady()
}

func (q *orderedPlanQueue) signalReady() {
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

// dequeue drains runnable ownership after admission closes. When only active
// Channels remain, their owning workers complete and drain their queued tails.
func (q *orderedPlanQueue) dequeue(stopReady <-chan struct{}) (onlinedelivery.RecipientDeliveryPlan, bool) {
	for {
		if plan, ok := q.pop(); ok {
			return plan, true
		}
		select {
		case <-q.ready:
		case <-stopReady:
			return q.pop()
		}
	}
}

func (q *orderedPlanQueue) pop() (onlinedelivery.RecipientDeliveryPlan, bool) {
	q.mu.Lock()
	channel := q.readyHead
	if channel == nil {
		q.mu.Unlock()
		return onlinedelivery.RecipientDeliveryPlan{}, false
	}
	q.readyHead = channel.nextReady
	channel.nextReady = nil
	if q.readyHead == nil {
		q.readyTail = nil
	} else {
		q.signalReady()
	}
	channel.active = true
	nodeIndex := channel.head
	node := &q.nodes[nodeIndex]
	plan := node.plan
	channel.head = node.next
	if channel.head == noPlanQueueNode {
		channel.tail = noPlanQueueNode
	}
	node.plan = onlinedelivery.RecipientDeliveryPlan{}
	node.next = q.freeHead
	q.freeHead = nodeIndex
	q.depth.Add(-1)
	q.mu.Unlock()
	q.slots <- struct{}{}
	return plan, true
}

// complete releases exact Channel execution ownership even after a plan
// failed or panicked, preserving FIFO without stranding its later pages.
func (q *orderedPlanQueue) complete(plan onlinedelivery.RecipientDeliveryPlan) {
	q.mu.Lock()
	defer q.mu.Unlock()
	key := planChannel(plan)
	channel := q.channels[key]
	channel.active = false
	if channel.head == noPlanQueueNode {
		delete(q.channels, key)
	} else {
		q.appendReady(channel)
	}
}

func (q *orderedPlanQueue) Depth() int {
	if q == nil {
		return 0
	}
	return int(q.depth.Load())
}

func (q *orderedPlanQueue) Capacity() int {
	if q == nil {
		return 0
	}
	return q.capacity
}
