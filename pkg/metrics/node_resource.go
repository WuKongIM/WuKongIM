package metrics

import "github.com/prometheus/client_golang/prometheus"

// NodeResourceObservation captures the latest process resource pressure for one node.
type NodeResourceObservation struct {
	// CPUPercent is process CPU usage since the previous sample. One fully busy CPU core is about 100.
	CPUPercent float64
	// MemoryRSSBytes is resident process memory in bytes.
	MemoryRSSBytes uint64
	// MemoryVMSBytes is virtual process memory in bytes.
	MemoryVMSBytes uint64
	// Goroutines is the current Go goroutine count.
	Goroutines int
	// Threads is the current OS thread count when available.
	Threads int
}

// NodeResourceMetrics exposes node-local process pressure gauges.
type NodeResourceMetrics struct {
	cpuPercent     prometheus.Gauge
	memoryRSSBytes prometheus.Gauge
	memoryVMSBytes prometheus.Gauge
	goroutines     prometheus.Gauge
	threads        prometheus.Gauge
}

func newNodeResourceMetrics(registry prometheus.Registerer, labels prometheus.Labels) *NodeResourceMetrics {
	m := &NodeResourceMetrics{
		cpuPercent: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_node_cpu_percent",
			Help:        "Latest process CPU usage percentage for this node. One fully busy CPU core is about 100.",
			ConstLabels: labels,
		}),
		memoryRSSBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_node_memory_rss_bytes",
			Help:        "Latest resident process memory in bytes for this node.",
			ConstLabels: labels,
		}),
		memoryVMSBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_node_memory_vms_bytes",
			Help:        "Latest virtual process memory in bytes for this node.",
			ConstLabels: labels,
		}),
		goroutines: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_node_goroutines",
			Help:        "Latest Go goroutine count for this node.",
			ConstLabels: labels,
		}),
		threads: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_node_threads",
			Help:        "Latest OS thread count for this node.",
			ConstLabels: labels,
		}),
	}

	registry.MustRegister(
		m.cpuPercent,
		m.memoryRSSBytes,
		m.memoryVMSBytes,
		m.goroutines,
		m.threads,
	)

	return m
}

// Set records the latest node resource pressure sample.
func (m *NodeResourceMetrics) Set(obs NodeResourceObservation) {
	if m == nil {
		return
	}
	m.cpuPercent.Set(clampNodeResourceFloat(obs.CPUPercent))
	m.memoryRSSBytes.Set(float64(obs.MemoryRSSBytes))
	m.memoryVMSBytes.Set(float64(obs.MemoryVMSBytes))
	m.goroutines.Set(float64(clampNodeResourceInt(obs.Goroutines)))
	m.threads.Set(float64(clampNodeResourceInt(obs.Threads)))
}

func clampNodeResourceFloat(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

func clampNodeResourceInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}
