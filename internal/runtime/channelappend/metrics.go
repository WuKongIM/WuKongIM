package channelappend

import (
	"context"
	"sync"
	"sync/atomic"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

// groupMetrics tracks aggregate channel-writer pressure without scanning shards.
type groupMetrics struct {
	observer WriterPressureObserver

	admissionShards   []*shard
	admissionCapacity int64
	appendPool        *workerPool
	postCommitPool    *workerPool
	advancePool       *workerPool
	advanceScheduler  *writerAdvanceScheduler
	handoff           *postCommitHandoff
	postCommitRetries *postCommitRetryScheduler

	pendingAppendItems  atomic.Int64
	appendInflightItems atomic.Int64
	postCommitBacklog   atomic.Int64

	pressureWake      chan struct{}
	pressureStop      chan struct{}
	pressureDone      chan struct{}
	pressureStarted   atomic.Bool
	pressureStopped   atomic.Bool
	pressureStartOnce sync.Once
	pressureStopOnce  sync.Once
}

func (m *groupMetrics) addPendingAppendItems(delta int) {
	if m == nil || delta == 0 {
		return
	}
	m.pendingAppendItems.Add(int64(delta))
}

func (m *groupMetrics) addAppendInflightItems(delta int) {
	if m == nil || delta == 0 {
		return
	}
	m.appendInflightItems.Add(int64(delta))
}

func (m *groupMetrics) addPostCommitBacklog(delta int) {
	if m == nil || delta == 0 {
		return
	}
	m.postCommitBacklog.Add(int64(delta))
}

func (m *groupMetrics) observePressure() {
	if m == nil || m.observer == nil {
		return
	}
	if !m.pressureStarted.Load() {
		// Directly constructed metrics values used by focused tests and
		// benchmarks retain synchronous observation. Production Groups start the
		// coalescing publisher before opening admission.
		m.publishPressureSnapshot()
		return
	}
	if m.pressureStopped.Load() {
		return
	}
	select {
	case m.pressureWake <- struct{}{}:
	default:
	}
}

// startPressurePublisher serializes pressure callbacks on one coalescing
// goroutine. Hot-path transitions only perform a non-blocking wakeup, so a slow
// observer cannot pin append or commit execution.
func (m *groupMetrics) startPressurePublisher() {
	if m == nil || m.observer == nil {
		return
	}
	m.pressureStartOnce.Do(func() {
		m.pressureWake = make(chan struct{}, 1)
		m.pressureStop = make(chan struct{})
		m.pressureDone = make(chan struct{})
		m.pressureStarted.Store(true)
		goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskChannelAppendMetrics, m.runPressurePublisher)
	})
}

func (m *groupMetrics) runPressurePublisher() {
	defer close(m.pressureDone)
	for {
		select {
		case <-m.pressureWake:
			m.publishPressureSnapshot()
		case <-m.pressureStop:
			// Stop is called after writer and retry state has drained. This final
			// serialized sample makes the terminal zero state observable even if
			// an earlier wakeup was coalesced.
			m.publishPressureSnapshot()
			return
		}
	}
}

func (m *groupMetrics) stopPressurePublisher(ctx context.Context) error {
	if m == nil || !m.pressureStarted.Load() {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.pressureStopOnce.Do(func() {
		m.pressureStopped.Store(true)
		close(m.pressureStop)
	})
	select {
	case <-m.pressureDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *groupMetrics) publishPressureSnapshot() {
	admissionUsed := m.admissionDepth()
	workerUsed, workerCapacity := 0, 0
	if m.appendPool != nil {
		workerUsed = m.appendPool.running()
		workerCapacity = m.appendPool.capacity()
	}
	handoffDepth, handoffCapacity := m.handoff.snapshot()
	retryQueueDepth, retryContended := m.postCommitRetries.snapshot()
	m.observer.SetChannelAppendWriterPressure(WriterPressureObservation{
		AdmissionDepth:            admissionUsed,
		AdmissionCapacity:         int(m.admissionCapacity),
		WorkerRunning:             workerUsed,
		WorkerCapacity:            workerCapacity,
		PendingAppendItems:        int(m.pendingAppendItems.Load()),
		AppendInflightItems:       int(m.appendInflightItems.Load()),
		PostCommitBacklog:         int(m.postCommitBacklog.Load()),
		PostCommitHandoffDepth:    handoffDepth,
		PostCommitHandoffCapacity: handoffCapacity,
		PostCommitRetryQueueDepth: retryQueueDepth,
		PostCommitRetryContended:  retryContended,
	})
	antsObserver, ok := m.observer.(AntsPoolObserver)
	if !ok || antsObserver == nil {
		return
	}
	observeWorkerPoolWithWaiting(antsObserver, "advance", m.advancePool, m.advanceScheduler.waiting())
	observeWorkerPool(antsObserver, "append_effect", m.appendPool)
	observeWorkerPool(antsObserver, "post_commit", m.postCommitPool)
}

func (m *groupMetrics) admissionDepth() int {
	if m == nil {
		return 0
	}
	depth := int64(0)
	for _, shard := range m.admissionShards {
		if shard == nil {
			continue
		}
		depth += shard.admissionUsed.Load()
	}
	return int(depth)
}

func observeWorkerPool(observer AntsPoolObserver, name string, pool *workerPool) {
	if pool == nil {
		return
	}
	observeWorkerPoolWithWaiting(observer, name, pool, pool.waiting())
}

func observeWorkerPoolWithWaiting(observer AntsPoolObserver, name string, pool *workerPool, waiting int) {
	if observer == nil || pool == nil {
		return
	}
	observer.ObserveChannelAppendAntsPool(AntsPoolObservation{
		Pool:     name,
		Running:  pool.running(),
		Capacity: pool.capacity(),
		Waiting:  waiting,
	})
}
