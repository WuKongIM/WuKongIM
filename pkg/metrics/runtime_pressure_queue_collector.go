package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// runtimePressureQueueCollector freezes each logical queue's four gauges before
// the registry serializes them. Collecting live Gauge references would allow
// depth and capacity to come from different valid publication revisions.
type runtimePressureQueueCollector struct {
	metrics *RuntimePressureMetrics
}

type runtimePressureQueueEntry struct {
	key    runtimePressureQueueKey
	series runtimePressureQueueSeries
}

func (c runtimePressureQueueCollector) Describe(out chan<- *prometheus.Desc) {
	for _, vector := range [...]*prometheus.GaugeVec{
		c.metrics.queueDepth, c.metrics.queueCapacity,
		c.metrics.queueBytes, c.metrics.queueBytesCapacity,
	} {
		if vector != nil {
			vector.Describe(out)
		}
	}
}

func (c runtimePressureQueueCollector) Collect(out chan<- prometheus.Metric) {
	m := c.metrics
	// Copy only fully bound series. Release the cache lock before acquiring any
	// publication lock, so collection cannot invert the publisher's lock order.
	m.seriesMu.RLock()
	entries := make([]runtimePressureQueueEntry, 0, len(m.queueSeries))
	for key, series := range m.queueSeries {
		entries = append(entries, runtimePressureQueueEntry{key: key, series: series})
	}
	m.seriesMu.RUnlock()

	for _, entry := range entries {
		var frozen [4]prometheus.Metric
		entry.series.publication.mu.Lock()
		for index, gauge := range [...]prometheus.Gauge{
			entry.series.depth, entry.series.capacity,
			entry.series.bytes, entry.series.bytesCapacity,
		} {
			if gauge == nil {
				continue
			}
			var value dto.Metric
			if err := gauge.Write(&value); err != nil {
				frozen[index] = prometheus.NewInvalidMetric(gauge.Desc(), err)
				continue
			}
			metric, err := prometheus.NewConstMetric(
				gauge.Desc(), prometheus.GaugeValue, value.GetGauge().GetValue(),
				entry.key.component, entry.key.pool, entry.key.queue, entry.key.priority,
			)
			if err != nil {
				metric = prometheus.NewInvalidMetric(gauge.Desc(), err)
			}
			frozen[index] = metric
		}
		entry.series.publication.mu.Unlock()
		// Registry writes happen later; immutable metrics retain this observation
		// without holding the publication lock while the consumer drains output.
		for _, metric := range frozen {
			if metric != nil {
				out <- metric
			}
		}
	}
}
