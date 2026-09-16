//go:build integration

// Package counterwindow retains bounded boundary evidence for integration benchmarks.
// It is excluded from ordinary product builds.
package counterwindow

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	runtimemetrics "runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// Start writes into an existing private directory and performs two boundary reads. There is no
// sampler goroutine, profiler or per-request work. The original gate owns its
// verdict; handlers_completed means the measurement ended, not that it passed.
func Start(b testing.TB, dir, schema, scope string, operations, rate int, gatherers ...prometheus.Gatherer) func() {
	b.Helper()
	before := Read(b, gatherers...)
	writeJSON(b, filepath.Join(dir, "before.json"), before)
	var once sync.Once
	finish := func(completed bool) {
		once.Do(func() {
			after := Read(b, gatherers...)
			writeJSON(b, filepath.Join(dir, "after.json"), after)
			writeJSON(b, filepath.Join(dir, "window.json"), map[string]any{
				"schema": schema, "handlers_completed": completed, "profile_enabled": false,
				"started_at": before.At, "ended_at": after.At, "counter_window_seconds": after.At.Sub(before.At).Seconds(),
				"operations": operations, "offered_qps": rate, "go_version": runtime.Version(),
				"gomaxprocs": runtime.GOMAXPROCS(0), "goos": runtime.GOOS, "goarch": runtime.GOARCH,
				"counter_scope": scope,
			})
		})
	}
	b.Cleanup(func() { finish(false) })
	return func() { finish(true) }
}

func writeJSON(b testing.TB, path string, value any) {
	b.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil || len(data) > 4<<20 {
		b.Fatalf("encode bounded counter snapshot: size=%d err=%v", len(data), err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		b.Fatal(err)
	}
}

// Snapshot owns one bounded, process-wide boundary observation. Families contain
// only fixed storage/replication series; differences exclude completed setup.
type Snapshot struct {
	At       time.Time           `json:"at"`
	System   map[string]string   `json:"system"`
	Missing  map[string]string   `json:"missing"`
	Families []*dto.MetricFamily `json:"families"`
	Runtime  map[string]any      `json:"runtime"`
}

// Read collects fixed system/runtime counters and selected metric families.
func Read(b testing.TB, gatherers ...prometheus.Gatherer) Snapshot {
	b.Helper()
	s := Snapshot{At: time.Now().UTC(), System: make(map[string]string), Missing: make(map[string]string), Runtime: runtimeCounters()}
	for _, path := range []string{
		"/proc/stat", "/proc/diskstats", "/proc/self/io", "/proc/self/cgroup",
		"/proc/pressure/cpu", "/proc/pressure/io", "/proc/pressure/memory",
		"/sys/fs/cgroup/cpu.stat", "/sys/fs/cgroup/cpu.max", "/sys/fs/cgroup/io.stat",
	} {
		data, err := ReadSystemFile(path)
		if err != nil {
			s.Missing[path] = err.Error()
		} else {
			s.System[path] = string(data)
		}
	}
	// On a host runner the process may live below the cgroup mount root. Retain
	// that scope separately; root counters must not masquerade as this job's quota.
	for _, line := range strings.Split(s.System["/proc/self/cgroup"], "\n") {
		if !strings.HasPrefix(line, "0::/") {
			continue
		}
		root := filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(line, "0::/"))
		for _, name := range []string{"cpu.stat", "cpu.max", "io.stat"} {
			path := filepath.Join(root, name)
			data, err := ReadSystemFile(path)
			if err != nil {
				s.Missing["process_cgroup/"+name] = err.Error()
			} else {
				s.System["process_cgroup/"+name] = string(data)
			}
		}
	}
	for _, gatherer := range gatherers {
		families, err := gatherer.Gather()
		if err != nil {
			b.Fatal(err)
		}
		for _, family := range families {
			// Closed metric families only; no message bodies or identities.
			switch family.GetName() {
			case "wukongim_channelv2_append_stage_duration_seconds",
				"wukongim_channelv2_append_wait_stage_duration_seconds",
				"wukongim_channelv2_replication_stage_duration_seconds",
				"wukongim_channelv2_worker_task_duration_seconds",
				"wukongim_storage_commit_batch_duration_seconds",
				"wukongim_storage_commit_request_duration_seconds":
				s.Families = append(s.Families, family)
			}
		}
	}
	return s
}

// ReadSystemFile caps one system pseudo-file at 64 KiB.
func ReadSystemFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if len(data) > 64<<10 {
		return nil, fmt.Errorf("system counter file exceeds 64 KiB")
	}
	return data, err
}

// runtimeCounters reads process-wide cumulative scheduler/GC counters.
// Histogram bounds use strings because runtime histograms include infinities.
func runtimeCounters() map[string]any {
	samples := []runtimemetrics.Sample{
		{Name: "/sched/latencies:seconds"}, {Name: "/gc/pauses:seconds"},
		{Name: "/cpu/classes/gc/total:cpu-seconds"}, {Name: "/cpu/classes/total:cpu-seconds"},
		{Name: "/gc/cycles/total:gc-cycles"},
	}
	runtimemetrics.Read(samples)
	result := make(map[string]any, len(samples))
	for _, sample := range samples {
		switch sample.Value.Kind() {
		case runtimemetrics.KindUint64:
			result[sample.Name] = sample.Value.Uint64()
		case runtimemetrics.KindFloat64:
			result[sample.Name] = sample.Value.Float64()
		case runtimemetrics.KindFloat64Histogram:
			h := sample.Value.Float64Histogram()
			bounds := make([]string, len(h.Buckets))
			for i, bound := range h.Buckets {
				bounds[i] = strconv.FormatFloat(bound, 'g', -1, 64)
			}
			result[sample.Name] = map[string]any{"bounds": bounds, "counts": append([]uint64(nil), h.Counts...)}
		default:
			result[sample.Name] = map[string]string{"missing": "runtime metric unavailable"}
		}
	}
	return result
}
