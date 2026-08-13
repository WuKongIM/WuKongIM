package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

var errHostMetricsConfig = errors.New("host metrics configuration failed")

const processMetricsFreshnessWindow = 45 * time.Second

type hostMetricsConfig struct {
	listen             string
	path               string
	mountpoint         string
	device             string
	systemPath         string
	watchPath          string
	processMetricsPath string
	physicalIO         bool
}

type hostMetricsHandler struct {
	path               string
	mountpoint         string
	device             string
	systemPath         string
	watchPath          string
	processMetricsPath string
	deviceIO           hostDeviceIOSampler

	mu             sync.Mutex
	previousCPU    hostCPUTotals
	previousCPUSet bool
	watchBytes     int64
	watchAt        time.Time
	watchValid     bool
}

type hostCPUTotals struct{ total, idle uint64 }

func newHostMetricsCommand(stderr io.Writer) *cobra.Command {
	cfg := hostMetricsConfig{}
	cmd := &cobra.Command{
		Use:   "host-metrics",
		Short: "Expose native filesystem evidence for local chat-lifecycle shakeouts",
		RunE: func(_ *cobra.Command, _ []string) error {
			handler, err := newHostMetricsHandler(cfg)
			if err != nil || strings.TrimSpace(cfg.listen) == "" {
				return commandExit{code: exitConfig, message: "--listen, --path, --mountpoint, and --device are required"}
			}
			server := &http.Server{
				Addr: cfg.listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
				ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
			}
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return commandExit{code: exitInternal, message: "host metrics server failed"}
			}
			return nil
		},
	}
	cmd.SetOut(stderr)
	cmd.SetErr(stderr)
	cmd.Flags().StringVar(&cfg.listen, "listen", "127.0.0.1:19101", "private HTTP listen address")
	cmd.Flags().StringVar(&cfg.path, "path", "", "existing data directory whose filesystem is measured")
	cmd.Flags().StringVar(&cfg.mountpoint, "mountpoint", "", "declared mountpoint label expected by the lifecycle config")
	cmd.Flags().StringVar(&cfg.device, "device", "", "declared device label expected by the lifecycle config")
	cmd.Flags().StringVar(&cfg.systemPath, "system-path", "/", "system filesystem path used by the five-percent safety guard")
	cmd.Flags().StringVar(&cfg.watchPath, "watch-path", "", "optional directory whose bounded size is exported")
	cmd.Flags().StringVar(&cfg.processMetricsPath, "process-metrics-path", "", "optional trusted process textfile forwarded as bounded evidence")
	cmd.Flags().BoolVar(&cfg.physicalIO, "physical-io", true, "sample the physical block device when supported")
	return cmd
}

func newHostMetricsHandler(cfg hostMetricsConfig) (http.Handler, error) {
	if strings.TrimSpace(cfg.path) == "" || strings.TrimSpace(cfg.mountpoint) == "" || strings.TrimSpace(cfg.device) == "" ||
		strings.ContainsAny(cfg.mountpoint, "\r\n") || strings.ContainsAny(cfg.device, "\r\n") {
		return nil, errHostMetricsConfig
	}
	absolute, err := filepath.Abs(cfg.path)
	if err != nil {
		return nil, errHostMetricsConfig
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return nil, errHostMetricsConfig
	}
	systemPath := strings.TrimSpace(cfg.systemPath)
	if systemPath == "" {
		systemPath = "/"
	}
	systemAbsolute, err := filepath.Abs(systemPath)
	if err != nil {
		return nil, errHostMetricsConfig
	}
	if info, err := os.Stat(systemAbsolute); err != nil || !info.IsDir() {
		return nil, errHostMetricsConfig
	}
	watchAbsolute := ""
	if strings.TrimSpace(cfg.watchPath) != "" {
		watchAbsolute, err = filepath.Abs(cfg.watchPath)
		if err != nil {
			return nil, errHostMetricsConfig
		}
	}
	processMetricsAbsolute := ""
	if strings.TrimSpace(cfg.processMetricsPath) != "" {
		processMetricsAbsolute, err = filepath.Abs(cfg.processMetricsPath)
		if err != nil {
			return nil, errHostMetricsConfig
		}
	}
	handler := &hostMetricsHandler{
		path: absolute, mountpoint: cfg.mountpoint, device: cfg.device, systemPath: systemAbsolute,
		watchPath: watchAbsolute, processMetricsPath: processMetricsAbsolute,
	}
	if cfg.physicalIO {
		handler.deviceIO = newHostDeviceIOSampler(absolute)
	} else {
		handler.deviceIO = unavailableHostDeviceIOSampler{}
	}
	if totals, ok := readHostCPUTotals(); ok {
		handler.previousCPU, handler.previousCPUSet = totals, true
	}
	return handler, nil
}

func (h *hostMetricsHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/healthz":
		writer.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(writer, "ok\n")
	case "/metrics":
		size, available, err := hostFilesystemBytes(h.path)
		if err != nil {
			http.Error(writer, "filesystem observation failed", http.StatusServiceUnavailable)
			return
		}
		processMetrics, err := readBoundedProcessMetrics(h.processMetricsPath, time.Now())
		if err != nil {
			http.Error(writer, "process observation failed", http.StatusServiceUnavailable)
			return
		}
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4")
		labels := fmt.Sprintf("device=%s,mountpoint=%s", strconv.Quote(h.device), strconv.Quote(h.mountpoint))
		_, _ = fmt.Fprintf(writer, "node_filesystem_size_bytes{%s} %d\n", labels, size)
		_, _ = fmt.Fprintf(writer, "node_filesystem_avail_bytes{%s} %d\n", labels, available)
		systemSize, systemAvailable, systemErr := hostFilesystemBytes(h.systemPath)
		if systemErr != nil {
			http.Error(writer, "system filesystem observation failed", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprintf(writer, "wkbench_host_system_filesystem_size_bytes %d\n", systemSize)
		_, _ = fmt.Fprintf(writer, "wkbench_host_system_filesystem_avail_bytes %d\n", systemAvailable)
		if cpu, ok := h.cpuPercent(); ok {
			_, _ = fmt.Fprintf(writer, "wkbench_host_cpu_busy_percent %.6f\n", cpu)
		}
		if memory, ok := hostMemoryUsedPercent(); ok {
			_, _ = fmt.Fprintf(writer, "wkbench_host_memory_used_percent %.6f\n", memory)
		}
		if transmitted, ok := hostNetworkTransmitBytes(); ok {
			_, _ = fmt.Fprintf(writer, "wkbench_host_network_transmit_bytes %d\n", transmitted)
		}
		if h.deviceIO != nil {
			writeHostDeviceIOMetrics(writer, h.deviceIO.Sample())
		}
		if bytes, ok := h.watchedBytes(time.Now()); ok {
			_, _ = fmt.Fprintf(writer, "wkbench_host_watched_directory_bytes %d\n", bytes)
		}
		if len(processMetrics) > 0 {
			_, _ = writer.Write(processMetrics)
			if processMetrics[len(processMetrics)-1] != '\n' {
				_, _ = io.WriteString(writer, "\n")
			}
		}
	default:
		http.NotFound(writer, request)
	}
}

func writeHostDeviceIOMetrics(writer io.Writer, sample hostDeviceIOSample) {
	device := sample.Device
	if strings.TrimSpace(device) == "" || strings.ContainsAny(device, "\r\n") {
		device = "unavailable"
	}
	deviceLabel := "physical_device=" + strconv.Quote(device)
	_, _ = fmt.Fprintf(writer, "wkbench_host_block_io_schema_info{%s,version=\"v1\"} 1\n", deviceLabel)
	availability := []struct {
		field string
		value bool
	}{
		{"iops", sample.IOPSAvailable},
		{"bytes_per_second", sample.BytesPerSecondAvailable},
		{"utilization", sample.UtilizationAvailable},
		{"service_time", sample.ServiceTimeAvailable},
		{"read_write_split", sample.ReadWriteSplitAvailable},
	}
	for _, entry := range availability {
		value := 0
		if entry.value {
			value = 1
		}
		_, _ = fmt.Fprintf(writer, "wkbench_host_block_io_available{field=%s,%s} %d\n", strconv.Quote(entry.field), deviceLabel, value)
	}
	if sample.IOPSAvailable {
		_, _ = fmt.Fprintf(writer, "wkbench_host_block_io_iops{operation=\"total\",%s} %.6f\n", deviceLabel, sample.TotalIOPS)
		if sample.ReadWriteSplitAvailable {
			_, _ = fmt.Fprintf(writer, "wkbench_host_block_io_iops{operation=\"read\",%s} %.6f\n", deviceLabel, sample.ReadIOPS)
			_, _ = fmt.Fprintf(writer, "wkbench_host_block_io_iops{operation=\"write\",%s} %.6f\n", deviceLabel, sample.WriteIOPS)
		}
	}
	if sample.BytesPerSecondAvailable {
		_, _ = fmt.Fprintf(writer, "wkbench_host_block_io_bytes_per_second{operation=\"total\",%s} %.6f\n", deviceLabel, sample.TotalBytesPerSecond)
		if sample.ReadWriteSplitAvailable {
			_, _ = fmt.Fprintf(writer, "wkbench_host_block_io_bytes_per_second{operation=\"read\",%s} %.6f\n", deviceLabel, sample.ReadBytesPerSecond)
			_, _ = fmt.Fprintf(writer, "wkbench_host_block_io_bytes_per_second{operation=\"write\",%s} %.6f\n", deviceLabel, sample.WriteBytesPerSecond)
		}
	}
	if sample.UtilizationAvailable {
		_, _ = fmt.Fprintf(writer, "wkbench_host_block_io_utilization_percent{%s} %.6f\n", deviceLabel, sample.UtilizationPercent)
	}
	if sample.ServiceTimeAvailable {
		_, _ = fmt.Fprintf(writer, "wkbench_host_block_io_service_time_milliseconds{%s} %.6f\n", deviceLabel, sample.ServiceTimeMilliseconds)
	}
}

func readBoundedProcessMetrics(path string, now time.Time) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errHostMetricsConfig
	}
	defer file.Close()
	info, statErr := file.Stat()
	linkInfo, linkErr := os.Lstat(path)
	if statErr != nil || linkErr != nil || !info.Mode().IsRegular() || !linkInfo.Mode().IsRegular() ||
		linkInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, linkInfo) || info.Size() < 0 || info.Size() > 256<<10 {
		return nil, errHostMetricsConfig
	}
	age := now.Sub(info.ModTime())
	if now.IsZero() || age < -5*time.Second || age > processMetricsFreshnessWindow {
		return nil, errHostMetricsConfig
	}
	body, err := io.ReadAll(io.LimitReader(file, (256<<10)+1))
	if err != nil || int64(len(body)) != info.Size() || strings.ContainsRune(string(body), '\r') {
		return nil, errHostMetricsConfig
	}
	lastSuccessSeen := false
	for _, line := range strings.Split(string(body), "\n") {
		if line == "" || strings.HasPrefix(line, "# ") {
			continue
		}
		if strings.HasPrefix(line, "wukongim_process_collector_last_success_unixtime_seconds ") {
			fields := strings.Fields(line)
			if lastSuccessSeen || len(fields) != 2 {
				return nil, errHostMetricsConfig
			}
			seconds, parseErr := strconv.ParseInt(fields[1], 10, 64)
			if parseErr != nil {
				return nil, errHostMetricsConfig
			}
			observedAt := time.Unix(seconds, 0)
			observedAge := now.Sub(observedAt)
			if observedAge < -5*time.Second || observedAge > processMetricsFreshnessWindow {
				return nil, errHostMetricsConfig
			}
			lastSuccessSeen = true
			continue
		}
		if !strings.HasPrefix(line, "wukongim_process_up{") &&
			!strings.HasPrefix(line, "wukongim_process_cpu_jiffies_total{") &&
			!strings.HasPrefix(line, "wukongim_process_resident_memory_bytes{") &&
			!strings.HasPrefix(line, "wukongim_process_threads{") &&
			!strings.HasPrefix(line, "wukongim_process_open_fds{") &&
			!strings.HasPrefix(line, "wukongim_process_read_bytes_total{") &&
			!strings.HasPrefix(line, "wukongim_process_write_bytes_total{") {
			return nil, errHostMetricsConfig
		}
	}
	if !lastSuccessSeen {
		return nil, errHostMetricsConfig
	}
	return body, nil
}

func (h *hostMetricsHandler) cpuPercent() (float64, bool) {
	current, ok := readHostCPUTotals()
	if !ok {
		return 0, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	previous, seen := h.previousCPU, h.previousCPUSet
	h.previousCPU, h.previousCPUSet = current, true
	if !seen || current.total <= previous.total || current.idle < previous.idle {
		return 0, false
	}
	total, idle := current.total-previous.total, current.idle-previous.idle
	if idle > total {
		return 0, false
	}
	return float64(total-idle) * 100 / float64(total), true
}

func readHostCPUTotals() (hostCPUTotals, bool) {
	times, err := cpu.Times(false)
	if err != nil || len(times) != 1 {
		return hostCPUTotals{}, false
	}
	sample := times[0]
	values := [...]float64{
		sample.User, sample.System, sample.Idle, sample.Nice, sample.Iowait,
		sample.Irq, sample.Softirq, sample.Steal,
	}
	var totalSeconds float64
	for _, value := range values {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return hostCPUTotals{}, false
		}
		totalSeconds += value
	}
	idleSeconds := sample.Idle + sample.Iowait
	total, totalOK := hostCPUSecondsToTicks(totalSeconds)
	idle, idleOK := hostCPUSecondsToTicks(idleSeconds)
	if !totalOK || !idleOK || total == 0 || idle > total {
		return hostCPUTotals{}, false
	}
	return hostCPUTotals{total: total, idle: idle}, true
}

func hostCPUSecondsToTicks(seconds float64) (uint64, bool) {
	const ticksPerSecond = 1_000_000_000
	if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds > float64(math.MaxUint64)/ticksPerSecond {
		return 0, false
	}
	return uint64(math.Round(seconds * ticksPerSecond)), true
}

func hostMemoryUsedPercent() (float64, bool) {
	memory, err := mem.VirtualMemory()
	if err != nil || memory.Total == 0 || memory.Available > memory.Total {
		return 0, false
	}
	return float64(memory.Total-memory.Available) * 100 / float64(memory.Total), true
}

func hostNetworkTransmitBytes() (uint64, bool) {
	body, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, false
	}
	var total uint64
	found := false
	for _, line := range strings.Split(string(body), "\n") {
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		fields := strings.Fields(line[colon+1:])
		if name == "" || name == "lo" || len(fields) < 16 {
			continue
		}
		value, parseErr := strconv.ParseUint(fields[8], 10, 64)
		if parseErr != nil || math.MaxUint64-total < value {
			return 0, false
		}
		total += value
		found = true
	}
	return total, found
}

func (h *hostMetricsHandler) watchedBytes(now time.Time) (int64, bool) {
	if h.watchPath == "" {
		return 0, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.watchValid && now.Sub(h.watchAt) < time.Minute {
		return h.watchBytes, true
	}
	bytes, err := boundedDirectoryBytes(h.watchPath)
	if err != nil {
		return 0, false
	}
	h.watchBytes, h.watchAt, h.watchValid = bytes, now, true
	return bytes, true
}

func boundedDirectoryBytes(root string) (int64, error) {
	var bytes int64
	entries := 0
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		entries++
		if entries > 1_000_000 {
			return errHostMetricsConfig
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil || info.Size() < 0 || math.MaxInt64-bytes < info.Size() {
				return errHostMetricsConfig
			}
			bytes += info.Size()
		}
		return nil
	})
	return bytes, err
}

func hostFilesystemBytes(path string) (int64, int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil || stat.Bsize <= 0 {
		return 0, 0, errHostMetricsConfig
	}
	sizeHigh, sizeLow := bits.Mul64(uint64(stat.Blocks), uint64(stat.Bsize))
	availableHigh, availableLow := bits.Mul64(uint64(stat.Bavail), uint64(stat.Bsize))
	if sizeHigh != 0 || availableHigh != 0 || sizeLow > math.MaxInt64 || availableLow > math.MaxInt64 || availableLow > sizeLow {
		return 0, 0, errHostMetricsConfig
	}
	return int64(sizeLow), int64(availableLow), nil
}
