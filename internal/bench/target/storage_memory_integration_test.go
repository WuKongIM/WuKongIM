//go:build integration

package target_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

const requiredStorageMetrics = `go_goroutines 10
go_memstats_heap_alloc_bytes 1024
process_resident_memory_bytes 2048
wukongim_runtime_pool_queue_depth{pool="append"} 0
wukongim_runtime_pool_queue_capacity{pool="append"} 10
wukongim_runtime_pool_inflight{pool="append"} 0
wukongim_channelv2_worker_queue_depth{worker="meta"} 0
wukongim_channelv2_worker_queue_capacity{worker="meta"} 10
wukongim_channelv2_activation_rejected_total{reason="capacity"} 0
wukongim_channelv2_meta_created_total{slot_id="1",result="created"} 0
wukongim_channelv2_meta_created_total{slot_id="1",result="already_existing"} 0
wukongim_channelv2_meta_created_total{slot_id="1",result="error"} 0
`

// The JSON projection is an additive public diagnostic value, not a private parser seam.
type storageMemoryHTTPValue struct {
	BlockCacheBytes       uint64 `json:"block_cache_bytes"`
	MemTableReservedBytes uint64 `json:"memtable_reserved_bytes"`
	BlockCacheKnown       bool   `json:"block_cache_known"`
	MemTableReservedKnown bool   `json:"memtable_reserved_known"`
}

func TestMetricsHTTPPreservesExistingStorageMemory(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/metrics" || r.Header.Get("Authorization") != "Bearer fixture-token" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, requiredStorageMetrics+`wukongim_storage_pebble_block_cache_size_bytes{store="channel_log",node_id="1",node_name="fixture"} 67108864
wukongim_storage_pebble_memtable_reserved_size_bytes{store="channel_log"} 1.34217728e+08
wukongim_storage_pebble_block_cache_size_bytes{store="meta"} 0
wukongim_storage_pebble_memtable_reserved_size_bytes{store="meta"} 33554432
wukongim_storage_pebble_block_cache_size_bytes{store="raft"} 4096
`)
	}))
	defer server.Close()
	client := target.NewClient(target.Config{APIAddrs: []string{server.URL}, Token: "fixture-token"})
	snapshot, err := client.Metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.ValidateRequired(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Memory []storageMemoryHTTPValue `json:"storage_memory"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		body, _ := json.Marshal(struct {
			Kind   string                   `json:"kind"`
			Passed bool                     `json:"passed"`
			Calls  int32                    `json:"http_calls"`
			Memory []storageMemoryHTTPValue `json:"storage_memory"`
		}{"existing_scrape_storage_memory_http", !t.Failed(), calls.Load(), result.Memory})
		t.Logf("storage_memory_http_report=%s", body)
	})
	want := []storageMemoryHTTPValue{
		{67108864, 134217728, true, true},
		{0, 33554432, true, true},
		{4096, 0, true, false},
	}
	if !reflect.DeepEqual(result.Memory, want) || calls.Load() != 1 {
		t.Fatalf("existing storage gauges lost or re-scraped: memory=%+v calls=%d, want %+v / 1", result.Memory, calls.Load(), want)
	}
}

func TestMetricsHTTPKeepsOptionalStorageMemoryUnknown(t *testing.T) {
	const cache = "wukongim_storage_pebble_block_cache_size_bytes"
	const reserved = "wukongim_storage_pebble_memtable_reserved_size_bytes"
	for _, tc := range []struct {
		name, optional string
		wantCache      uint64
		known          bool
	}{
		{"missing", "", 0, false},
		{"zero", cache + `{store="channel_log"} 0`, 0, true},
		{"duplicate", cache + `{store="channel_log"} 10` + "\n" + cache + `{store="channel_log"} 10`, 0, false},
		{"third_cannot_repair_duplicate", cache + `{store="channel_log"} 10` + "\n" + cache + `{store="channel_log"} 11` + "\n" + cache + `{store="channel_log"} 12`, 0, false},
		{"nan", cache + `{store="channel_log"} NaN`, 0, false},
		{"infinite", cache + `{store="channel_log"} +Inf`, 0, false},
		{"huge_negative_exponent", cache + `{store="channel_log"} 1e-1000000000`, 0, false},
		{"huge_exponent_exact_zero", cache + `{store="channel_log"} 0e1000000000`, 0, true},
		{"negative", cache + `{store="channel_log"} -1`, 0, false},
		{"fraction", cache + `{store="channel_log"} 1.5`, 0, false},
		{"tiny_fraction_must_not_round_to_integer", cache + `{store="channel_log"} 1.` + strings.Repeat("0", 100) + "1", 0, false},
		{"overflow", cache + `{store="channel_log"} 18446744073709551616`, 0, false},
		{"inexact_float_boundary", cache + `{store="channel_log"} 9007199254740993`, 0, false},
		{"unknown_store", cache + `{store="raw-sensitive-channel"} 10`, 0, false},
		{"unknown_label", cache + `{store="channel_log",uid="raw-sensitive-user"} 10`, 0, false},
		{"invalid_label_after_valid", cache + `{store="channel_log"} 10` + "\n" + cache + `{store="unterminated} 1`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, requiredStorageMetrics+tc.optional+"\n"+reserved+`{store="meta"} 8192`+"\n")
			}))
			defer server.Close()
			got, err := target.NewClient(target.Config{APIAddrs: []string{server.URL}}).Metrics(context.Background())
			if err != nil {
				t.Fatalf("optional evidence changed original acquisition: %v", err)
			}
			if err := got.ValidateRequired(); err != nil {
				t.Fatal(err)
			}
			if got.StorageMemory[0].BlockCacheKnown != tc.known || got.StorageMemory[0].BlockCacheBytes != tc.wantCache || !got.StorageMemory[1].MemTableReservedKnown || got.StorageMemory[1].MemTableReservedBytes != 8192 {
				t.Fatalf("invalid optional evidence projection: %+v", got.StorageMemory)
			}
			body, _ := json.Marshal(got.StorageMemory)
			if bytes.Contains(body, []byte("raw-sensitive")) {
				t.Fatal("retained unbounded identity label")
			}
		})
	}
}

func TestMetricsHTTPRejectsHugeExponentWithoutExpansion(t *testing.T) {
	// This fixed short response must not expand into a multi-MiB integer. The
	// isolated2MiB allowance includes HTTP/parser noise, not a native heap gate.
	const allowance = uint64(2 << 20)
	var large atomic.Bool
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		value := "1"
		if large.Load() {
			value = "1e10000000"
		}
		_, _ = io.WriteString(w, requiredStorageMetrics+`wukongim_storage_pebble_block_cache_size_bytes{store="channel_log"} `+value+"\n")
	}))
	defer server.Close()
	client := target.NewClient(target.Config{APIAddrs: []string{server.URL}})
	if _, err := client.Metrics(context.Background()); err != nil {
		t.Fatal(err)
	}
	large.Store(true)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got, err := client.Metrics(context.Background())
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Cleanup(func() {
		body, _ := json.Marshal(map[string]any{"kind": "optional_storage_exponent_resource_bound", "passed": !t.Failed(), "http_calls": calls.Load(), "allocated_bytes": allocated, "allowance_bytes": allowance, "native_heap_cause_proved": false})
		t.Logf("storage_exponent_report=%s", body)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := got.ValidateRequired(); err != nil {
		t.Fatal(err)
	}
	if got.StorageMemory[0].BlockCacheKnown || allocated > allowance || calls.Load() != 2 {
		t.Fatalf("overflow expanded or changed required path: known=%v allocated=%d allowance=%d calls=%d", got.StorageMemory[0].BlockCacheKnown, allocated, allowance, calls.Load())
	}
}
