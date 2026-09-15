//go:build integration

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	runtimemetrics "runtime/metrics"
	"strconv"
	"sync"
	"testing"
)

// startMixedSendCounterWindow performs only two boundary reads. There is no
// sampler goroutine, profiler or per-request work. The original gate owns its
// verdict; handlers_completed means the measurement ended, not that it passed.
func startMixedSendCounterWindow(b testing.TB, apps []*App, dir string, operations, rate int) func() {
	b.Helper()
	before := mixedSendDiagnosticSnapshot(b, apps)
	writeMixedSendCounterJSON(b, filepath.Join(dir, "before.json"), before)
	var once sync.Once
	finish := func(completed bool) {
		once.Do(func() {
			after := mixedSendDiagnosticSnapshot(b, apps)
			writeMixedSendCounterJSON(b, filepath.Join(dir, "after.json"), after)
			writeMixedSendCounterJSON(b, filepath.Join(dir, "window.json"), map[string]any{
				"schema": "mixed-send-counters/v1", "handlers_completed": completed, "profile_enabled": false,
				"started_at": before.At, "ended_at": after.At, "counter_window_seconds": after.At.Sub(before.At).Seconds(),
				"operations": operations, "offered_qps": rate, "go_version": runtime.Version(),
				"gomaxprocs": runtime.GOMAXPROCS(0), "goos": runtime.GOOS, "goarch": runtime.GOARCH,
				"counter_scope": "post-warmup through completed handlers; includes boundary snapshot and benchmark timer overhead",
			})
		})
	}
	b.Cleanup(func() { finish(false) })
	return func() { finish(true) }
}

func writeMixedSendCounterJSON(b testing.TB, path string, value any) {
	b.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil || len(data) > 4<<20 {
		b.Fatalf("encode bounded counter snapshot: size=%d err=%v", len(data), err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		b.Fatal(err)
	}
}

// mixedSendRuntimeCounters reads process-wide cumulative scheduler/GC counters.
// Histogram bounds use strings because runtime histograms include infinities.
func mixedSendRuntimeCounters() map[string]any {
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
