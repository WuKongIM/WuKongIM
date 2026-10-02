//go:build e2e && linux

package suite

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LinuxProcessObservation reports process counters separately from the enclosing
// cgroup, which can also contain the test driver and its charged page cache.
type LinuxProcessObservation struct {
	PeakRSSBytes       uint64 `json:"peak_rss_bytes"`
	ReadBytes          uint64 `json:"read_bytes"`
	WriteBytes         uint64 `json:"write_bytes"`
	ReadChars          uint64 `json:"read_chars"`
	WriteChars         uint64 `json:"write_chars"`
	CPUUserTicks       uint64 `json:"cpu_user_ticks"`
	CPUSystemTicks     uint64 `json:"cpu_system_ticks"`
	Samples            uint64 `json:"samples"`
	MemoryMax          string `json:"cgroup_memory_max"`
	SwapMax            string `json:"cgroup_swap_max"`
	MemoryEventsBefore string `json:"cgroup_memory_events_before"`
	MemoryEventsAfter  string `json:"cgroup_memory_events_after"`
	CgroupMemoryPeak   string `json:"cgroup_memory_peak"`
}

// LinuxProcessMonitor owns a bounded sampler joined by Stop.
type LinuxProcessMonitor struct {
	pid    int
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
	result LinuxProcessObservation
}

// ObserveLinuxProcess samples public Linux process counters every 20ms. It does
// not inspect application storage or treat Go's soft heap limit as a hard cap.
func ObserveLinuxProcess(pid int) (*LinuxProcessMonitor, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid process id")
	}
	if _, err := os.Stat(fmt.Sprintf("/proc/%d/status", pid)); err != nil {
		return nil, err
	}
	m := &LinuxProcessMonitor{pid: pid, stop: make(chan struct{}), done: make(chan struct{})}
	m.result.MemoryMax = readCounterFile("/sys/fs/cgroup/memory.max")
	m.result.SwapMax = readCounterFile("/sys/fs/cgroup/memory.swap.max")
	m.result.MemoryEventsBefore = readCounterFile("/sys/fs/cgroup/memory.events")
	go func() {
		defer close(m.done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		m.sample()
		for {
			select {
			case <-ticker.C:
				m.sample()
			case <-m.stop:
				m.sample()
				return
			}
		}
	}()
	return m, nil
}

// Stop returns a stable result after the sampling goroutine has exited.
func (m *LinuxProcessMonitor) Stop() LinuxProcessObservation {
	m.once.Do(func() {
		close(m.stop)
		<-m.done
		m.result.MemoryEventsAfter = readCounterFile("/sys/fs/cgroup/memory.events")
		m.result.CgroupMemoryPeak = readCounterFile("/sys/fs/cgroup/memory.peak")
	})
	return m.result
}

func (m *LinuxProcessMonitor) sample() {
	base := fmt.Sprintf("/proc/%d/", m.pid)
	status, err := os.ReadFile(base + "status")
	if err != nil {
		return
	}
	m.result.Samples++
	for _, line := range strings.Split(string(status), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && (fields[0] == "VmRSS:" || fields[0] == "VmHWM:") {
			value, _ := strconv.ParseUint(fields[1], 10, 64)
			m.result.PeakRSSBytes = max(m.result.PeakRSSBytes, value*1024)
		}
	}
	stats, _ := os.ReadFile(base + "stat")
	// The comm field can contain spaces and parentheses; fields after its final
	// closing parenthesis start with state (field 3), then utime at offset 11.
	if end := strings.LastIndexByte(string(stats), ')'); end >= 0 {
		fields := strings.Fields(string(stats)[end+1:])
		if len(fields) > 12 {
			m.result.CPUUserTicks, _ = strconv.ParseUint(fields[11], 10, 64)
			m.result.CPUSystemTicks, _ = strconv.ParseUint(fields[12], 10, 64)
		}
	}
	ioStats, _ := os.ReadFile(base + "io")
	for _, line := range strings.Split(string(ioStats), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		value, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "read_bytes:":
			m.result.ReadBytes = value
		case "write_bytes:":
			m.result.WriteBytes = value
		case "rchar:":
			m.result.ReadChars = value
		case "wchar:":
			m.result.WriteChars = value
		}
	}
}

func readCounterFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "unavailable"
	}
	return strings.TrimSpace(string(data))
}

// CaptureRecoveryProfiles captures bounded public diagnostics after readiness.
// Failures remain explicit artifacts instead of silently claiming profile data.
func CaptureRecoveryProfiles(ctx context.Context, baseURL, dir string) {
	for _, item := range []struct{ name, path string }{{"heap.pprof", "/debug/pprof/heap"}, {"cpu.pprof", "/debug/pprof/profile?seconds=1"}, {"metrics.txt", "/metrics"}} {
		captureCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		req, err := http.NewRequestWithContext(captureCtx, http.MethodGet, baseURL+item.path, nil)
		var data []byte
		if err == nil {
			resp, e := http.DefaultClient.Do(req)
			err = e
			if err == nil {
				if resp.StatusCode != http.StatusOK {
					err = fmt.Errorf("diagnostic status %d", resp.StatusCode)
				} else {
					data, err = io.ReadAll(io.LimitReader(resp.Body, 16<<20))
				}
				resp.Body.Close()
			}
		}
		cancel()
		if err != nil {
			_ = os.WriteFile(filepath.Join(dir, item.name+".error"), []byte(err.Error()), 0600)
		} else {
			_ = os.WriteFile(filepath.Join(dir, item.name), data, 0600)
		}
	}
}
