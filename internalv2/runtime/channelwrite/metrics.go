package channelwrite

import "sync/atomic"

// groupMetrics tracks aggregate channel-writer pressure without scanning shards.
type groupMetrics struct {
	observer WriterPressureObserver

	admissionUsed     *atomic.Int64
	admissionCapacity int64
	pool              *workerPool

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
	admissionUsed := 0
	if m.admissionUsed != nil {
		admissionUsed = int(m.admissionUsed.Load())
	}
	workerUsed, workerCapacity := 0, 0
	if m.pool != nil {
		workerUsed = m.pool.running()
		workerCapacity = m.pool.capacity()
	}
	m.observer.SetChannelWriteWriterPressure(WriterPressureObservation{
		AdmissionDepth:      admissionUsed,
		AdmissionCapacity:   int(m.admissionCapacity),
		WorkerRunning:       workerUsed,
		WorkerCapacity:      workerCapacity,
		PendingAppendItems:  int(m.pendingAppendItems.Load()),
		AppendInflightItems: int(m.appendInflightItems.Load()),
		PostCommitBacklog:   int(m.postCommitBacklog.Load()),
	})
}
