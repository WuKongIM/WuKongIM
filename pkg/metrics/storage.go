package metrics

import "github.com/prometheus/client_golang/prometheus"

var storageStores = []string{
	"meta",
	"raft",
	"channel_log",
	"controller_meta",
	"controller_raft",
}

type StorageMetrics struct {
	diskUsageBytes *prometheus.GaugeVec
}

func newStorageMetrics(registry prometheus.Registerer, labels prometheus.Labels) *StorageMetrics {
	m := &StorageMetrics{
		diskUsageBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_disk_usage_bytes",
			Help:        "Disk usage by storage subsystem in bytes.",
			ConstLabels: labels,
		}, []string{"store"}),
	}

	registry.MustRegister(m.diskUsageBytes)

	for _, store := range storageStores {
		m.diskUsageBytes.WithLabelValues(store).Set(0)
	}

	return m
}

func (m *StorageMetrics) SetDiskUsage(usageByStore map[string]int64) {
	if m == nil {
		return
	}

	known := make(map[string]struct{}, len(storageStores))
	for _, store := range storageStores {
		known[store] = struct{}{}
		m.diskUsageBytes.WithLabelValues(store).Set(float64(usageByStore[store]))
	}
	for store, size := range usageByStore {
		if _, ok := known[store]; ok {
			continue
		}
		m.diskUsageBytes.WithLabelValues(store).Set(float64(size))
	}
}
