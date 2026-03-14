package main

import (
	"slices"
	"sync/atomic"
	"time"
)

// benchMetrics collects benchmark statistics using atomic counters.
type benchMetrics struct {
	totalSent    atomic.Int64 // Messages sent
	totalRecv    atomic.Int64 // Messages received
	totalSendErr atomic.Int64 // Send errors
	elapsed      time.Duration
	payloadLen   int

	// Populated by finalize()
	sendRate float64       // msgs/s
	recvRate float64       // msgs/s
	sendMBps float64       // MB/s
	recvMBps float64       // MB/s
	lossRate float64       // %
	latMin   time.Duration // min latency
	latAvg   time.Duration // avg latency
	latP50   time.Duration // P50 latency
	latP95   time.Duration // P95 latency
	latP99   time.Duration // P99 latency
	latMax   time.Duration // max latency
	latCount int           // total latency samples
}

// finalize merges all per-pair latency buffers and computes final statistics.
// Must be called after all goroutines have finished.
func (m *benchMetrics) finalize(pairs []*benchPair) {
	secs := m.elapsed.Seconds()
	if secs <= 0 {
		secs = 1
	}

	sent := m.totalSent.Load()
	recv := m.totalRecv.Load()

	m.sendRate = float64(sent) / secs
	m.recvRate = float64(recv) / secs
	m.sendMBps = float64(sent) * float64(m.payloadLen) / secs / (1024 * 1024)
	m.recvMBps = float64(recv) * float64(m.payloadLen) / secs / (1024 * 1024)

	if sent > 0 {
		m.lossRate = float64(sent-recv) / float64(sent) * 100
		if m.lossRate < 0 {
			m.lossRate = 0
		}
	}

	// Merge all latency samples.
	total := 0
	for _, p := range pairs {
		total += len(p.latBuf)
	}
	if total == 0 {
		return
	}

	all := make([]int64, 0, total)
	for _, p := range pairs {
		all = append(all, p.latBuf...)
	}
	slices.Sort(all)

	m.latCount = len(all)
	n := len(all)

	m.latMin = time.Duration(all[0])
	m.latMax = time.Duration(all[n-1])

	var sum int64
	for _, v := range all {
		sum += v
	}
	m.latAvg = time.Duration(sum / int64(n))

	m.latP50 = time.Duration(all[percentileIndex(n, 50)])
	m.latP95 = time.Duration(all[percentileIndex(n, 95)])
	m.latP99 = time.Duration(all[percentileIndex(n, 99)])
}

// percentileIndex returns the index for the given percentile in a sorted slice of length n.
func percentileIndex(n, pct int) int {
	idx := n * pct / 100
	if idx >= n {
		idx = n - 1
	}
	return idx
}
