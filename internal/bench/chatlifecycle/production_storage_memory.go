package chatlifecycle

import (
	"encoding/json"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

// storageMemoryNode is a fixed same-scrape diagnostic projection, never a gate input.
// Store order is channel_log/meta/raft; producer age and evicted reader pins are unknown.
type storageMemoryNode struct {
	Position int                            `json:"node_position"`
	Heap     uint64                         `json:"heap_alloc_bytes"`
	RSS      uint64                         `json:"resident_memory_bytes"`
	RSSKnown bool                           `json:"resident_memory_known"`
	Memory   [3]target.StorageMemoryMetrics `json:"storage_memory"`
}

// writeStorageMemoryDiagnostic uses only an already committed hourly GC round.
// Its separate 16x4KiB allowance cannot consume the existing failure-log budget.
func (s *ProductionObservationSource) writeStorageMemoryDiagnostic(at, start time.Time, metrics [coordinatorWorkerCount]target.MetricsSnapshot) {
	if s.diagnosticLog == nil {
		return
	}
	var nodes [coordinatorWorkerCount]storageMemoryNode
	for i, m := range metrics {
		heap, _ := productionGauge(m.GoHeapAllocBytes) // commitRound already validated this field.
		rss, rssKnown := productionGauge(m.ProcessResidentMemoryBytes)
		nodes[i] = storageMemoryNode{Position: i, Heap: heap, RSS: rss, RSSKnown: rssKnown, Memory: m.StorageMemory}
		for store := range nodes[i].Memory {
			value := &nodes[i].Memory[store]
			// External target adapters cannot turn unknown or inexact optional bytes into evidence.
			if !value.BlockCacheKnown || value.BlockCacheBytes > 1<<53 {
				value.BlockCacheBytes, value.BlockCacheKnown = 0, false
			}
			if !value.MemTableReservedKnown || value.MemTableReservedBytes > 1<<53 {
				value.MemTableReservedBytes, value.MemTableReservedKnown = 0, false
			}
		}
	}
	body, err := json.Marshal(struct {
		Event               string                                    `json:"event"`
		Elapsed             int64                                     `json:"elapsed_ns"`
		GaugeSourceAgeKnown bool                                      `json:"gauge_source_age_known"`
		Nodes               [coordinatorWorkerCount]storageMemoryNode `json:"nodes"`
	}{Event: "wkbench.chat_lifecycle.storage_memory_gc", Elapsed: int64(at.Sub(start)), Nodes: nodes})
	if err != nil || len(body)+1 > 4<<10 {
		return
	}
	body = append(body, '\n')
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	if s.storageMemoryLines >= 16 {
		return
	}
	s.storageMemoryLines++
	_, _ = s.diagnosticLog.Write(body)
}
