package channelappend

import "sync/atomic"

// groupMetrics tracks aggregate channel-writer pressure without scanning shards.
type groupMetrics struct {
	observer WriterPressureObserver

	admissionShards   []*shard
	admissionCapacity int64
	appendPool        *workerPool
	postCommitPool    *workerPool
	advancePool       *workerPool

	pendingAppendItems  atomic.Int64
	appendInflightItems atomic.Int64
	postCommitBacklog   atomic.Int64
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
	if m == nil {
		return
	}
	if m.observer == nil {
		return
	}
	admissionUsed := m.admissionDepth()
	workerUsed, workerCapacity := 0, 0
	if m.appendPool != nil {
		workerUsed = m.appendPool.running()
		workerCapacity = m.appendPool.capacity()
	}
	m.observer.SetChannelAppendWriterPressure(WriterPressureObservation{
		AdmissionDepth:      admissionUsed,
		AdmissionCapacity:   int(m.admissionCapacity),
		WorkerRunning:       workerUsed,
		WorkerCapacity:      workerCapacity,
		PendingAppendItems:  int(m.pendingAppendItems.Load()),
		AppendInflightItems: int(m.appendInflightItems.Load()),
		PostCommitBacklog:   int(m.postCommitBacklog.Load()),
	})
	antsObserver, ok := m.observer.(AntsPoolObserver)
	if !ok || antsObserver == nil {
		return
	}
	observeWorkerPool(antsObserver, "advance", m.advancePool)
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
	if observer == nil || pool == nil {
		return
	}
	observer.ObserveChannelAppendAntsPool(AntsPoolObservation{
		Pool:     name,
		Running:  pool.running(),
		Capacity: pool.capacity(),
		Waiting:  pool.waiting(),
	})
}
