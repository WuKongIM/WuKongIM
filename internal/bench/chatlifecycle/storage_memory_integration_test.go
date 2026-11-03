//go:build integration

package chatlifecycle_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	cl "github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

type storageMemoryTarget struct {
	metrics               target.MetricsSnapshot
	gcCalls, metricsCalls int
	err                   error
}

func (s *storageMemoryTarget) ForceGC(context.Context) error { s.gcCalls++; return s.err }
func (s *storageMemoryTarget) Metrics(context.Context) (target.MetricsSnapshot, error) {
	s.metricsCalls++
	return s.metrics, s.err
}

type storageMemoryDisk struct{ value cl.DataFilesystem }

func (d storageMemoryDisk) Filesystem(context.Context) (cl.DataFilesystem, error) {
	return d.value, nil
}

func storageMemorySample(at time.Time) cl.ObserverSample {
	s := cl.ObserverSample{At: at, ServiceHealthy: true, ClusterHealthy: true, LogicalSlotGroups: 12, LeaderGroups: 12, FullReplicaGroups: 12}
	for node := range s.Nodes {
		s.Nodes[node] = target.DebugCluster{NodeID: uint64(node + 1), StateRevision: 1}
		for slot := 0; slot < 12; slot++ {
			leader := uint64(slot%3 + 1)
			v := target.ClusterSlot{SlotID: uint32(slot + 1), LeaderID: leader, Replicas: []uint64{1, 2, 3}, Voters: []uint64{1, 2, 3}, Term: 1, CommitIndex: 100, AppliedIndex: 100}
			if leader == uint64(node+1) {
				for peer := uint64(1); peer <= 3; peer++ {
					v.ReplicaProgress = append(v.ReplicaProgress, target.ReplicaProgress{NodeID: peer, MatchIndex: 100, State: "StateReplicate"})
				}
			}
			s.Nodes[node].Slots = append(s.Nodes[node].Slots, v)
		}
	}
	return s
}

func newStorageMemorySource(t *testing.T, writer io.Writer) (*cl.ProductionObservationSource, []*storageMemoryTarget) {
	t.Helper()
	cfg := cl.LocalConfig()
	targets := make([]*storageMemoryTarget, 3)
	for i := range targets {
		targets[i] = &storageMemoryTarget{metrics: target.MetricsSnapshot{GoGoroutines: 10, GoHeapAllocBytes: float64(1024 + i), ProcessResidentMemoryBytes: 2048, RuntimeQueueCapacity: 100, ChannelWorkerQueueCapacity: 100}}
		targets[i].metrics.StorageMemory[0] = target.StorageMemoryMetrics{BlockCacheBytes: uint64(4096 + i), BlockCacheKnown: true, MemTableReservedBytes: 8192, MemTableReservedKnown: true}
	}
	source, err := cl.NewProductionObservationSource(cl.ProductionObservationOptions{
		Config: cfg, BenchToken: "fixture-token", DiagnosticLog: writer,
		TargetFactory: func(i int, _ cl.EndpointDeclaration, _ string) cl.ProductionObservationTarget { return targets[i] },
		DiskFactory: func(i int, _ cl.EndpointDeclaration) cl.DiskReader {
			fs := cl.DataFilesystem{SizeBytes: 1_000_000_000_000, AvailableBytes: 900_000_000_000, SystemSizeBytes: 1_000_000_000_000, SystemAvailableBytes: 900_000_000_000, CPUPercent: 10, MemoryPercent: 30, HostResourcesObserved: true, WatchedDirectoryObserved: true, NetworkTransmitObserved: true, ProcessResourcesObserved: true}
			fs.ProcessUp[1] = true
			if i < 3 {
				fs.ProcessUp[0] = true
			} else {
				for j := 2; j <= 5; j++ {
					fs.ProcessUp[j] = true
				}
			}
			return storageMemoryDisk{value: fs}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return source, targets
}

func TestStorageMemoryEvidenceSanitizesUnknownExternalValues(t *testing.T) {
	var output bytes.Buffer
	source, targets := newStorageMemorySource(t, &output)
	targets[0].metrics.StorageMemory[1] = target.StorageMemoryMetrics{BlockCacheBytes: 99, MemTableReservedBytes: ^uint64(0), MemTableReservedKnown: true}
	start := time.Date(2030, 3, 17, 17, 0, 0, 0, time.UTC)
	if err := source.Begin(start); err != nil {
		t.Fatal(err)
	}
	if err := source.Observe(context.Background(), storageMemorySample(start)); err != nil {
		t.Fatal(err)
	}
	var line struct {
		Nodes []struct {
			Memory [3]target.StorageMemoryMetrics `json:"storage_memory"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &line); err != nil {
		t.Fatal(err)
	}
	if len(line.Nodes) != 3 || line.Nodes[0].Memory[1] != (target.StorageMemoryMetrics{}) {
		t.Fatalf("untrusted optional evidence became known bytes: %+v", line.Nodes)
	}
}

func TestStorageMemoryEvidenceHasIndependentBoundedAllowance(t *testing.T) {
	var output bytes.Buffer
	source, targets := newStorageMemorySource(t, &output)
	start := time.Date(2030, 3, 17, 17, 0, 0, 0, time.UTC)
	if err := source.Begin(start); err != nil {
		t.Fatal(err)
	}
	for hour := 0; hour < 18; hour++ {
		if err := source.Observe(context.Background(), storageMemorySample(start.Add(time.Duration(hour)*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 80; i++ {
		if err := source.Observe(context.Background(), storageMemorySample(start)); err == nil {
			t.Fatal("old invalid-sample guard changed")
		}
	}
	memory, failure, memoryBytes := 0, 0, 0
	for _, line := range bytes.Split(output.Bytes(), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var value struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		if value.Event == "wkbench.chat_lifecycle.storage_memory_gc" {
			memory++
			memoryBytes += len(line) + 1
			if len(line)+1 > 4<<10 {
				t.Fatal("memory line exceeds4KiB")
			}
		} else {
			failure++
		}
	}
	if memory != 16 || memoryBytes > 64<<10 || failure != 64 {
		t.Fatalf("memory/failure budgets=%d/%d bytes=%d, want16/64 bounded independently", memory, failure, memoryBytes)
	}
	for _, target := range targets {
		if target.gcCalls != 18 || target.metricsCalls != 18 {
			t.Fatal("diagnostic allowance changed collection")
		}
	}
	t.Logf(`storage_memory_budget_report={"passed":true,"memory_lines":%d,"memory_bytes":%d,"original_diagnostic_lines":%d}`, memory, memoryBytes, failure)
}

type storageMemoryFailWriter struct{ memoryCalls int }

func (w *storageMemoryFailWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(`"event":"wkbench.chat_lifecycle.storage_memory_gc"`)) {
		w.memoryCalls++
	}
	return 0, errors.New("fixture write failure")
}

func TestStorageMemoryEvidenceWriterFailureDoesNotChangeObservation(t *testing.T) {
	w := &storageMemoryFailWriter{}
	source, _ := newStorageMemorySource(t, w)
	start := time.Date(2030, 3, 17, 17, 0, 0, 0, time.UTC)
	if err := source.Begin(start); err != nil {
		t.Fatal(err)
	}
	for hour := 0; hour < 18; hour++ {
		if err := source.Observe(context.Background(), storageMemorySample(start.Add(time.Duration(hour)*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	if w.memoryCalls != 16 || source.Snapshot().Sequence != 18 {
		t.Fatalf("writer failures changed observation or bound: writes=%d seq=%d", w.memoryCalls, source.Snapshot().Sequence)
	}
}

func TestStorageMemoryEvidenceOmitsFailedAndIncompleteRounds(t *testing.T) {
	for _, kind := range []string{"failed", "incomplete"} {
		t.Run(kind, func(t *testing.T) {
			var output bytes.Buffer
			source, targets := newStorageMemorySource(t, &output)
			if kind == "failed" {
				targets[0].err = errors.New("fixture unavailable")
			} else {
				targets[0].metrics.RuntimeQueueMaxPercent = 101
			}
			start := time.Date(2030, 3, 17, 17, 0, 0, 0, time.UTC)
			if err := source.Begin(start); err != nil {
				t.Fatal(err)
			}
			err := source.Observe(context.Background(), storageMemorySample(start))
			if (err != nil) != (kind == "failed") {
				t.Fatalf("original observation outcome changed: %v", err)
			}
			if bytes.Contains(output.Bytes(), []byte(`"event":"wkbench.chat_lifecycle.storage_memory_gc"`)) {
				t.Fatal("failed/incomplete round published successful memory evidence")
			}
		})
	}
}

func TestStorageMemoryEvidenceUsesOnlyExistingGCRounds(t *testing.T) {
	var output bytes.Buffer
	source, targets := newStorageMemorySource(t, &output)
	start := time.Date(2030, 3, 17, 17, 0, 0, 0, time.UTC)
	if err := source.Begin(start); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{start, start.Add(5 * time.Second), start.Add(time.Hour)} {
		if err := source.Observe(context.Background(), storageMemorySample(at)); err != nil {
			t.Fatal(err)
		}
	}
	var lines []map[string]json.RawMessage
	for _, line := range bytes.Split(output.Bytes(), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var value map[string]json.RawMessage
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		var event string
		_ = json.Unmarshal(value["event"], &event)
		if event == "wkbench.chat_lifecycle.storage_memory_gc" {
			lines = append(lines, value)
		}
	}
	t.Cleanup(func() {
		body, _ := json.Marshal(map[string]any{"kind": "existing_gc_storage_memory", "passed": !t.Failed(), "memory_lines": len(lines), "observation_rounds": source.Snapshot().Sequence})
		t.Logf("storage_memory_gc_report=%s", body)
	})
	if len(lines) != 2 {
		t.Fatalf("existing GC memory evidence lines=%d, want2", len(lines))
	}
	for i, value := range lines {
		var elapsed int64
		if err := json.Unmarshal(value["elapsed_ns"], &elapsed); err != nil || elapsed != int64(i)*int64(time.Hour) {
			t.Fatalf("elapsed=%d err=%v", elapsed, err)
		}
		var nodes []struct {
			Heap   uint64                         `json:"heap_alloc_bytes"`
			Memory [3]target.StorageMemoryMetrics `json:"storage_memory"`
		}
		if err := json.Unmarshal(value["nodes"], &nodes); err != nil {
			t.Fatal(err)
		}
		if len(nodes) != 3 || nodes[0].Heap != 1024 || nodes[2].Memory[0].BlockCacheBytes != 4098 || !nodes[0].Memory[0].BlockCacheKnown || nodes[0].Memory[1].BlockCacheKnown {
			t.Fatalf("invalid fixed round projection: %+v", nodes)
		}
	}
	for _, v := range targets {
		if v.gcCalls != 2 || v.metricsCalls != 3 {
			t.Fatalf("extra requests gc=%d metrics=%d", v.gcCalls, v.metricsCalls)
		}
	}
	snapshot := source.Snapshot()
	snapshot.Metrics[0].StorageMemory[0].BlockCacheBytes = 9
	if source.Snapshot().Metrics[0].StorageMemory[0].BlockCacheBytes != 4096 {
		t.Fatal("snapshot aliases memory values")
	}
}
