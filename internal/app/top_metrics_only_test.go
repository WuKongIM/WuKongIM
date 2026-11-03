package app

import (
	"testing"
	"time"

	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// Top-disabled tests must keep acceptance metrics fresh without keeping a
// hidden Top ring or invoking Top cluster snapshots and alert history.
func TestTopDisabledSamplesMetricsWithoutRetainingHistory(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "metrics-only"
		if enabled {
			name = "top-enabled-control"
		}
		t.Run(name, func(t *testing.T) {
			a := &App{cfg: Config{Top: TopConfig{APIEnabled: enabled}, Observability: ObservabilityConfig{MetricsEnabled: true}}}
			cc := clusterpkg.Config{NodeID: 5}
			a.configureObservability(&cc)
			c, ok := a.top.(*topCollector)
			if !ok || a.metrics == nil {
				t.Fatal("resource sampler and metrics must remain composed")
			}
			if (a.topProvider != nil) != enabled {
				t.Fatal("Top provider does not follow API flag")
			}
			var clusterCalls, resourceCalls, storageCalls int
			c.options.ClusterSnapshot = func() clusterpkg.Snapshot { clusterCalls++; return clusterpkg.Snapshot{NodeID: 5} }
			var resource topResourceSample
			c.options.ResourceSampler = func() topResourceSample { resourceCalls++; return resource }
			var snapshot clusterpkg.StorageMetricsSnapshot
			c.options.StorageMetricsSnapshot = func() clusterpkg.StorageMetricsSnapshot { storageCalls++; return snapshot }
			for pass := 0; pass < 3; pass++ {
				resource = topResourceSample{CPUPercent: float64(pass+1) * 12.5, MemoryRSSBytes: uint64(pass+1) * 4096, MemoryVMSBytes: 65536, Goroutines: 7, Threads: 3}
				cache := int64(100)
				if pass == 2 {
					resource = topResourceSample{}
					cache = 0
				}
				snapshot = clusterpkg.StorageMetricsSnapshot{Stores: []clusterpkg.StorageStoreMetricsSnapshot{{Store: "meta", Engine: clusterpkg.StorageEngineMetrics{BlockCacheSizeBytes: cache}}}}
				c.recordSampleAt(time.Unix(100+int64(pass), 0))
				families, err := a.metrics.Gather()
				if err != nil {
					t.Fatal(err)
				}
				for metric, want := range map[string]float64{
					"wukongim_node_cpu_percent":      resource.CPUPercent,
					"wukongim_node_memory_rss_bytes": float64(resource.MemoryRSSBytes),
					"wukongim_node_memory_vms_bytes": float64(resource.MemoryVMSBytes),
					"wukongim_node_goroutines":       float64(resource.Goroutines),
					"wukongim_node_threads":          float64(resource.Threads),
				} {
					f := requireAppMetricFamily(t, families, metric)
					if len(f.Metric) != 1 || f.Metric[0].GetGauge().GetValue() != want {
						t.Fatalf("pass%d %s not refreshed to%v", pass, metric, want)
					}
				}
				f := requireAppMetricFamily(t, families, "wukongim_storage_pebble_block_cache_size_bytes")
				if got := findAppMetricByLabels(t, f, map[string]string{"store": "meta"}).GetGauge().GetValue(); got != float64(cache) {
					t.Fatalf("storage cache=%v,want%d", got, cache)
				}
			}
			if resourceCalls != 3 || storageCalls != 3 {
				t.Fatalf("resource/storage calls=%d/%d,want3/3", resourceCalls, storageCalls)
			}
			if !enabled {
				if len(c.ring) != 0 || c.count != 0 || clusterCalls != 0 {
					t.Fatalf("Top disabled still retained/sampled history: ring=%d,count=%d,cluster_calls=%d", len(c.ring), c.count, clusterCalls)
				}
				if len(c.histos) != 0 || len(c.alerts) != 0 {
					t.Fatal("Top disabled retained histograms or alerts")
				}
			} else if len(c.ring) == 0 || c.count != 3 || clusterCalls != 3 {
				t.Fatal("Top enabled control lost history")
			}
		})
	}
}
