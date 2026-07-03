package app

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/legacy/contracts/deliveryevents"
)

const defaultDeliveryAckBatchMaxSize = 64

type deliveryAckBatchNotifierOptions struct {
	// FlushDelay is the maximum time one remote delivery ack may wait for peers.
	FlushDelay time.Duration
	// MaxBatch flushes a node batch immediately when the pending ack count reaches it.
	MaxBatch int
}

type deliveryAckBatchSender interface {
	deliveryOwnerNotifier
	NotifyAckBatch(ctx context.Context, nodeID uint64, commands []deliveryevents.RouteAck) error
}

type deliveryAckBatchNotifier struct {
	sender     deliveryAckBatchSender
	flushDelay time.Duration
	maxBatch   int

	mu      sync.Mutex
	closed  bool
	batches map[uint64]*deliveryAckPendingBatch
}

type deliveryAckPendingBatch struct {
	timer   *time.Timer
	waiters []*deliveryAckWaiter
}

type deliveryAckWaiter struct {
	command deliveryevents.RouteAck
	done    chan error
}

func newDeliveryAckBatchNotifier(sender deliveryAckBatchSender, opts deliveryAckBatchNotifierOptions) *deliveryAckBatchNotifier {
	maxBatch := opts.MaxBatch
	if maxBatch <= 0 {
		maxBatch = defaultDeliveryAckBatchMaxSize
	}
	return &deliveryAckBatchNotifier{
		sender:     sender,
		flushDelay: opts.FlushDelay,
		maxBatch:   maxBatch,
		batches:    make(map[uint64]*deliveryAckPendingBatch),
	}
}

func (n *deliveryAckBatchNotifier) NotifyAck(ctx context.Context, nodeID uint64, cmd deliveryevents.RouteAck) error {
	if n == nil || n.sender == nil {
		return errRemoteAckNotifierRequired
	}
	if n.flushDelay <= 0 || n.maxBatch <= 1 {
		return n.sender.NotifyAck(ctx, nodeID, cmd)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	waiter := &deliveryAckWaiter{command: cmd, done: make(chan error, 1)}
	var ready []*deliveryAckWaiter

	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return context.Canceled
	}
	batch := n.batches[nodeID]
	if batch == nil {
		batch = &deliveryAckPendingBatch{}
		n.batches[nodeID] = batch
		batch.timer = time.AfterFunc(n.flushDelay, func() {
			n.flushNode(nodeID)
		})
	}
	batch.waiters = append(batch.waiters, waiter)
	if len(batch.waiters) >= n.maxBatch {
		ready = n.detachNodeLocked(nodeID)
	}
	n.mu.Unlock()

	if len(ready) > 0 {
		go n.flushWaiters(nodeID, ready)
	}
	return nil
}

func (n *deliveryAckBatchNotifier) NotifyOffline(ctx context.Context, nodeID uint64, cmd deliveryevents.SessionClosed) error {
	if n == nil || n.sender == nil {
		return errRemoteAckNotifierRequired
	}
	return n.sender.NotifyOffline(ctx, nodeID, cmd)
}

func (n *deliveryAckBatchNotifier) Close() {
	if n == nil {
		return
	}
	n.mu.Lock()
	n.closed = true
	pending := n.detachAllLocked()
	n.mu.Unlock()
	for nodeID, waiters := range pending {
		n.flushWaiters(nodeID, waiters)
	}
}

func (n *deliveryAckBatchNotifier) flushNode(nodeID uint64) {
	n.mu.Lock()
	waiters := n.detachNodeLocked(nodeID)
	n.mu.Unlock()
	n.flushWaiters(nodeID, waiters)
}

func (n *deliveryAckBatchNotifier) flushWaiters(nodeID uint64, waiters []*deliveryAckWaiter) {
	if len(waiters) == 0 {
		return
	}
	commands := make([]deliveryevents.RouteAck, 0, len(waiters))
	for _, waiter := range waiters {
		commands = append(commands, waiter.command)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := n.sender.NotifyAckBatch(ctx, nodeID, commands)
	cancel()
	for _, waiter := range waiters {
		waiter.done <- err
	}
}

func (n *deliveryAckBatchNotifier) cancelWaiter(nodeID uint64, waiter *deliveryAckWaiter) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	batch := n.batches[nodeID]
	if batch == nil {
		return false
	}
	for i, pending := range batch.waiters {
		if pending != waiter {
			continue
		}
		batch.waiters = append(batch.waiters[:i], batch.waiters[i+1:]...)
		if len(batch.waiters) == 0 {
			_ = n.detachNodeLocked(nodeID)
		}
		return true
	}
	return false
}

func (n *deliveryAckBatchNotifier) detachNodeLocked(nodeID uint64) []*deliveryAckWaiter {
	batch := n.batches[nodeID]
	if batch == nil {
		return nil
	}
	delete(n.batches, nodeID)
	if batch.timer != nil {
		batch.timer.Stop()
	}
	return batch.waiters
}

func (n *deliveryAckBatchNotifier) detachAllLocked() map[uint64][]*deliveryAckWaiter {
	out := make(map[uint64][]*deliveryAckWaiter, len(n.batches))
	for nodeID := range n.batches {
		waiters := n.detachNodeLocked(nodeID)
		if len(waiters) > 0 {
			out[nodeID] = waiters
		}
	}
	return out
}
