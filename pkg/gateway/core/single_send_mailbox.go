package core

import (
	"context"
	"sync"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/workqueue"
)

// sendMailbox accepts records that already hold the original global and hash-shard
// credits. Close runs only after accepted work has drained.
type sendMailbox interface {
	SubmitHash(context.Context, uint64, asyncDispatchTask) error
	Close(context.Context) error
}

// singleSendMailbox schedules single-record preparation by ordering key on one
// fixed worker group. A hash collision shares admission credits, not a worker.
type singleSendMailbox struct {
	executor *sendExecutor
	homes    []singleSendHome
	workers  *workqueue.BoundedWorkerQueue[*singleSendLane]
}

type singleSendHome struct {
	mu    sync.Mutex
	lanes map[deferredSendLaneKey]*singleSendLane
}

type singleSendLane struct {
	shard int
	key   deferredSendLaneKey
	first asyncDispatchTask
	// pending holds only admitted records awaiting this exact ordering key.
	pending []asyncDispatchTask
}

func newSingleSendMailbox(e *sendExecutor) (*singleSendMailbox, error) {
	m := &singleSendMailbox{executor: e, homes: make([]singleSendHome, e.shards)}
	workers, err := workqueue.NewBoundedWorkerQueue(workqueue.BoundedWorkerQueueConfig{
		Name: "gateway-send", Goroutines: e.goroutines, Task: goruntimeregistry.TaskGatewayAsyncDispatch,
		Workers: e.workers, QueueSize: e.capacity,
	}, m.drainLane)
	if err != nil {
		return nil, err
	}
	m.workers = workers
	return m, nil
}

// SubmitHash queues one ready lane per active key. Keyless traffic uses the
// original serial mailbox for its shard, including independent protocol packets.
func (m *singleSendMailbox) SubmitHash(ctx context.Context, hash uint64, task asyncDispatchTask) error {
	shard := int(hash % uint64(len(m.homes)))
	home := &m.homes[shard]
	var key deferredSendLaneKey
	if task.orderKey.ID != "" {
		key = deferredSendLaneKey{state: task.state, order: task.orderKey}
	}
	home.mu.Lock()
	defer home.mu.Unlock()
	if lane := home.lanes[key]; lane != nil {
		lane.pending = append(lane.pending, task)
		return nil
	}
	if home.lanes == nil {
		home.lanes = make(map[deferredSendLaneKey]*singleSendLane)
	}
	lane := &singleSendLane{shard: shard, key: key, first: task}
	if err := m.workers.Submit(ctx, lane); err != nil {
		return err
	}
	home.lanes[key] = lane
	return nil
}

func (m *singleSendMailbox) drainLane(ctx context.Context, lane *singleSendLane) error {
	home := &m.homes[lane.shard]
	items := []asyncDispatchTask{lane.first}
	lane.first = asyncDispatchTask{}
	for {
		m.dispatchOne(ctx, lane.shard, items)
		home.mu.Lock()
		if len(lane.pending) == 0 {
			delete(home.lanes, lane.key)
			home.mu.Unlock()
			return nil
		}
		items[0] = lane.pending[0]
		lane.pending[0] = asyncDispatchTask{}
		lane.pending = lane.pending[1:]
		home.mu.Unlock()
	}
}

// Keep one escaping callback panic from retiring a shared worker or stranding
// the next record on this ordering key.
func (m *singleSendMailbox) dispatchOne(ctx context.Context, shard int, items []asyncDispatchTask) {
	defer func() {
		if v := recover(); v != nil {
			m.executor.recordPanic(v, items[0])
		}
	}()
	m.executor.handleMailboxBatch(ctx, workqueue.MailboxBatch[asyncDispatchTask]{Shard: shard, Items: items})
}

func (m *singleSendMailbox) Close(ctx context.Context) error { return m.workers.Close(ctx) }
