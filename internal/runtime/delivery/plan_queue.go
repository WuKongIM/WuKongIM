package delivery

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
)

const noPlanQueueNode = -1

// orderedPlanQueue preserves FIFO execution for one Channel shard while one
// global semaphore keeps aggregate queued plan ownership strictly bounded.
// Each shard has exactly one runtime worker, so unrelated Channels retain the
// configured worker parallelism without allowing a later same-Channel plan to
// overtake an earlier plan during presence resolution or owner push.
type orderedPlanQueue struct {
	// capacity is the node-wide queued-plan ownership bound.
	capacity int
	// shards assigns exactly one FIFO to each runtime worker.
	shards []orderedPlanShard
	// nodes preallocates all queue links and retained plan slots.
	nodes []orderedPlanNode
	// slots accounts aggregate free nodes before admission mutates a shard.
	slots chan struct{}

	// mu protects every shard link and the shared free-node list.
	mu sync.Mutex
	// freeHead is the first unused preallocated node.
	freeHead int
	// depth publishes aggregate queued plans without taking mu.
	depth atomic.Int64
}

type orderedPlanShard struct {
	head  int
	tail  int
	ready chan struct{}
}

type orderedPlanNode struct {
	plan onlinedelivery.RecipientDeliveryPlan
	next int
}

// newOrderedPlanQueue constructs a fixed-capacity queue with one FIFO per
// worker. It performs every retained-node allocation before the runtime starts.
func newOrderedPlanQueue(capacity, shards int) *orderedPlanQueue {
	if capacity <= 0 || shards <= 0 {
		return nil
	}
	queue := &orderedPlanQueue{
		capacity: capacity,
		shards:   make([]orderedPlanShard, shards),
		nodes:    make([]orderedPlanNode, capacity),
		slots:    make(chan struct{}, capacity),
		freeHead: 0,
	}
	for index := range queue.shards {
		queue.shards[index] = orderedPlanShard{head: noPlanQueueNode, tail: noPlanQueueNode, ready: make(chan struct{}, 1)}
	}
	for index := range queue.nodes {
		queue.nodes[index].next = index + 1
		queue.slots <- struct{}{}
	}
	queue.nodes[len(queue.nodes)-1].next = noPlanQueueNode
	return queue
}

// enqueue waits for global capacity, then transfers immutable plan ownership
// to its canonical Channel shard while admission remains open.
func (q *orderedPlanQueue) enqueue(
	ctx context.Context,
	acceptDone <-chan struct{},
	plan onlinedelivery.RecipientDeliveryPlan,
) error {
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
	// Stop can close admission at the same instant a capacity token becomes
	// ready. Rechecking after acquisition prevents that select race from
	// retaining a plan outside the lifecycle admission wait group.
	select {
	case <-acceptDone:
		q.releaseSlot()
		return ErrRuntimeClosed
	default:
	}

	shardIndex := q.shardIndex(plan)
	q.mu.Lock()
	nodeIndex := q.freeHead
	if nodeIndex == noPlanQueueNode {
		q.mu.Unlock()
		q.releaseSlot()
		return ErrRuntimeClosed
	}
	node := &q.nodes[nodeIndex]
	q.freeHead = node.next
	node.plan = plan
	node.next = noPlanQueueNode
	shard := &q.shards[shardIndex]
	if shard.tail == noPlanQueueNode {
		shard.head, shard.tail = nodeIndex, nodeIndex
	} else {
		q.nodes[shard.tail].next = nodeIndex
		shard.tail = nodeIndex
	}
	q.depth.Add(1)
	q.mu.Unlock()
	select {
	case shard.ready <- struct{}{}:
	default:
	}
	return nil
}

// dequeue returns the next plan for one worker shard and drains accepted work
// after stopReady closes.
func (q *orderedPlanQueue) dequeue(shardIndex int, stopReady <-chan struct{}) (onlinedelivery.RecipientDeliveryPlan, bool) {
	if q == nil || shardIndex < 0 || shardIndex >= len(q.shards) {
		return onlinedelivery.RecipientDeliveryPlan{}, false
	}
	for {
		if plan, ok := q.pop(shardIndex); ok {
			return plan, true
		}
		select {
		case <-q.shards[shardIndex].ready:
		case <-stopReady:
			if plan, ok := q.pop(shardIndex); ok {
				return plan, true
			}
			return onlinedelivery.RecipientDeliveryPlan{}, false
		}
	}
}

func (q *orderedPlanQueue) pop(shardIndex int) (onlinedelivery.RecipientDeliveryPlan, bool) {
	q.mu.Lock()
	shard := &q.shards[shardIndex]
	nodeIndex := shard.head
	if nodeIndex == noPlanQueueNode {
		q.mu.Unlock()
		return onlinedelivery.RecipientDeliveryPlan{}, false
	}
	node := &q.nodes[nodeIndex]
	plan := node.plan
	shard.head = node.next
	if shard.head == noPlanQueueNode {
		shard.tail = noPlanQueueNode
	}
	node.plan = onlinedelivery.RecipientDeliveryPlan{}
	node.next = q.freeHead
	q.freeHead = nodeIndex
	q.depth.Add(-1)
	q.mu.Unlock()
	q.releaseSlot()
	return plan, true
}

func (q *orderedPlanQueue) releaseSlot() {
	q.slots <- struct{}{}
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

// shardIndex maps all plans for one canonical Channel to the same worker.
func (q *orderedPlanQueue) shardIndex(plan onlinedelivery.RecipientDeliveryPlan) int {
	const (
		fnvOffset64 = uint64(14695981039346656037)
		fnvPrime64  = uint64(1099511628211)
	)
	hash := (fnvOffset64 ^ uint64(plan.Event.ChannelType)) * fnvPrime64
	for index := 0; index < len(plan.Event.ChannelID); index++ {
		hash = (hash ^ uint64(plan.Event.ChannelID[index])) * fnvPrime64
	}
	return int(hash % uint64(len(q.shards)))
}
