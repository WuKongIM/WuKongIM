//go:build integration

package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"strings"
	"sync"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

// startMixedSendDiagnostics selects counter-only or profiled evidence for the
// fixed benchmark. Collection excludes setup and warmup. All three nodes
// and the driver share one process, so CPU samples are not per-node CPU usage.
func startMixedSendDiagnostics(b *testing.B, apps []*App, rate int) func() {
	dir := os.Getenv("WK_BENCH_SEND_DIAGNOSTICS_DIR")
	countersDir := os.Getenv("WK_BENCH_SEND_COUNTERS_DIR")
	if dir != "" && countersDir != "" {
		b.Fatal("SEND counter-only and profiled diagnostics are mutually exclusive")
	}
	if countersDir != "" {
		dir = countersDir
	}
	if dir == "" {
		return func() {}
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || runtime.GOMAXPROCS(0) != 4 || b.N != 3000 || rate != 500 || b.Name() != "BenchmarkThreeNodeMixedSendPath500QPS" {
		b.Fatal("SEND diagnostics require Linux amd64, GOMAXPROCS=4 and exactly 3000 operations at 500 QPS")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		b.Fatalf("create fresh diagnostic directory: %v", err)
	}
	if countersDir != "" {
		return startMixedSendCounterWindow(b, apps, dir, b.N, rate)
	}
	writeSnapshot := func(name string) {
		snapshot := mixedSendDiagnosticSnapshot(b, apps)
		data, err := json.MarshalIndent(snapshot, "", "  ")
		if err != nil || len(data) > 4<<20 {
			b.Fatalf("encode bounded diagnostic snapshot: size=%d err=%v", len(data), err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".json"), data, 0600); err != nil {
			b.Fatal(err)
		}
	}
	writeSnapshot("before")
	// Keep profiler writes off the benchmark's data disk. The launcher verifies
	// /dev/shm is tmpfs; profiles are copied to retained evidence after sampling.
	profileDir, err := os.MkdirTemp("/dev/shm", "wk-send-profile-")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(profileDir) })
	cpu := newMixedSendDiagnosticFile(b, filepath.Join(profileDir, "cpu.pprof"), 8<<20)
	execution := newMixedSendDiagnosticFile(b, filepath.Join(profileDir, "execution.trace"), 64<<20)
	if err := pprof.StartCPUProfile(cpu); err != nil {
		b.Fatal(err)
	}
	if err := trace.Start(execution); err != nil {
		pprof.StopCPUProfile()
		b.Fatal(err)
	}
	started := time.Now()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			ended := time.Now()
			trace.Stop()
			pprof.StopCPUProfile()
			for _, output := range []*mixedSendDiagnosticFile{cpu, execution} {
				if err := output.file.Close(); err != nil {
					b.Error(err)
				}
				if output.overflow {
					b.Error("diagnostic profile exceeded its byte budget")
				}
				if output.err != nil {
					b.Error(output.err)
				}
			}
			writeSnapshot("after")
			for _, output := range []*mixedSendDiagnosticFile{cpu, execution} {
				src, err := os.Open(output.file.Name())
				if err != nil {
					b.Fatal(err)
				}
				dst := newMixedSendDiagnosticFile(b, filepath.Join(dir, filepath.Base(output.file.Name())), output.limit)
				_, copyErr := io.Copy(dst, src)
				_ = src.Close()
				closeErr := dst.file.Close()
				if copyErr != nil || closeErr != nil {
					b.Fatalf("retain diagnostic profile: copy=%v close=%v", copyErr, closeErr)
				}
			}
			data, err := json.Marshal(map[string]any{
				"schema": "mixed-send-window/v1", "started_at": started.UTC(), "ended_at": ended.UTC(),
				"profile_window_seconds": ended.Sub(started).Seconds(), "operations": b.N, "offered_qps": rate,
				"profile_complete": !cpu.overflow && !execution.overflow && cpu.err == nil && execution.err == nil,
				"cpu_bytes":        cpu.written, "trace_bytes": execution.written,
				"counter_scope": "before profile start through profile stop; includes profiler shutdown overhead",
			})
			if err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "window.json"), data, 0600); err != nil {
				b.Fatal(err)
			}
		})
	}
	b.Cleanup(stop)
	return stop
}

// mixedSendDiagnosticFile bounds profiler output without buffering it in RAM.
// Each profiler serializes its writes; fields are read only after it stops.
type mixedSendDiagnosticFile struct {
	file     *os.File
	limit    int
	written  int
	overflow bool
	err      error
}

func newMixedSendDiagnosticFile(b testing.TB, path string, limit int) *mixedSendDiagnosticFile {
	b.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = f.Close() })
	return &mixedSendDiagnosticFile{file: f, limit: limit}
}

func (f *mixedSendDiagnosticFile) Write(data []byte) (int, error) {
	if len(data) > f.limit-f.written {
		f.overflow = true
		return 0, fmt.Errorf("profile byte limit exceeded")
	}
	n, err := f.file.Write(data)
	f.written += n
	if err != nil {
		f.err = err
	}
	return n, err
}

type mixedSendSnapshot struct {
	At       time.Time           `json:"at"`
	System   map[string]string   `json:"system"`
	Missing  map[string]string   `json:"missing"`
	Families []*dto.MetricFamily `json:"families"`
	Runtime  map[string]any      `json:"runtime"`
}

func mixedSendDiagnosticSnapshot(b testing.TB, apps []*App) mixedSendSnapshot {
	b.Helper()
	s := mixedSendSnapshot{At: time.Now().UTC(), System: make(map[string]string), Missing: make(map[string]string), Runtime: mixedSendRuntimeCounters()}
	for _, path := range []string{
		"/proc/stat", "/proc/diskstats", "/proc/self/io", "/proc/self/cgroup",
		"/proc/pressure/cpu", "/proc/pressure/io", "/proc/pressure/memory",
		"/sys/fs/cgroup/cpu.stat", "/sys/fs/cgroup/cpu.max", "/sys/fs/cgroup/io.stat",
	} {
		data, err := readMixedSendDiagnosticFile(path)
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
			data, err := readMixedSendDiagnosticFile(path)
			if err != nil {
				s.Missing["process_cgroup/"+name] = err.Error()
			} else {
				s.System["process_cgroup/"+name] = string(data)
			}
		}
	}
	for _, app := range apps {
		families, err := app.metrics.PrometheusRegistry().Gather()
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

func readMixedSendDiagnosticFile(path string) ([]byte, error) {
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
