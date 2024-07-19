package reactor

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type MessageQueue struct {
	rl    *RateLimiter // 速率限制
	queue []Message

	mu sync.Mutex

	wklog.Log
}

func NewMessageQueue(size uint64, maxMemorySize uint64) *MessageQueue {
	q := &MessageQueue{
		Log:   wklog.NewWKLog("MessageQueue"),
		rl:    NewRateLimiter(maxMemorySize),
		queue: make([]Message, 0, size),
	}
	return q
}

// Add adds the specified message to the queue.
func (q *MessageQueue) Add(msg Message) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.rl.Enabled() {
		if q.rl.RateLimited() {
			q.Warn("rate limited dropped a Replicate msg")
			return false
		}
	}
	q.queue = append(q.queue, msg)
	return true
}

// Get returns everything current in the queue.
func (q *MessageQueue) Get() []Message {
	q.mu.Lock()
	defer q.mu.Unlock()

	result := q.queue
	q.queue = nil // Clear the queue

	return result
}
