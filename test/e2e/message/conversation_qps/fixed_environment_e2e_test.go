//go:build e2e

package conversation_qps

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

type fixedHostFile struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

type fixedHostCgroup struct {
	Location fixedHostCgroupLocation             `json:"location"`
	Controls map[string]map[string]fixedHostFile `json:"visible_ancestor_controls"`
	Scope    string                              `json:"scope"`
}

type fixedHostProcess struct {
	PID            int             `json:"pid"`
	StartTicks     uint64          `json:"starttime_ticks"`
	Affinity       string          `json:"cpu_affinity"`
	Stat           fixedHostFile   `json:"proc_stat"`
	Status         fixedHostFile   `json:"proc_status"`
	Membership     fixedHostFile   `json:"proc_cgroup"`
	MountInfo      fixedHostFile   `json:"proc_mountinfo"`
	Cgroup         fixedHostCgroup `json:"cgroup_v2"`
	BinaryPath     string          `json:"binary_path,omitempty"`
	BinarySHA256   string          `json:"binary_sha256,omitempty"`
	ConfigPath     string          `json:"config_path,omitempty"`
	ConfigSHA256   string          `json:"config_sha256,omitempty"`
	RedactedConfig string          `json:"redacted_toml,omitempty"`
	EstimatedStart time.Time       `json:"estimated_start,omitempty"`
}

type fixedHostStage struct {
	Name       string    `json:"name"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	DurationNS int64     `json:"duration_ns"`
	Failed     bool      `json:"failed"`
}

type fixedHostMetricCut struct {
	NodeID  uint64                    `json:"node_id"`
	Scrape  suite.MetricScrapeReceipt `json:"scrape"`
	Samples []suite.MetricSample      `json:"selected_samples"`
	Error   string                    `json:"error,omitempty"`
}

type fixedHostBoundary struct {
	Name       string               `json:"name"`
	StartedAt  time.Time            `json:"started_at"`
	FinishedAt time.Time            `json:"finished_at"`
	Metrics    []fixedHostMetricCut `json:"metrics"`
}

type fixedHostResourceCut struct {
	Name       string             `json:"name"`
	StartedAt  time.Time          `json:"started_at"`
	FinishedAt time.Time          `json:"finished_at"`
	Processes  []fixedHostProcess `json:"processes"`
}

// fixedHostReceipt separates the controlled preparation release point from the
// subsequent unchanged warmups and actual request-window starts.
type fixedHostReceipt struct {
	Schema                     string                 `json:"schema"`
	Valid                      bool                   `json:"valid"`
	Failure                    string                 `json:"failure,omitempty"`
	TargetAgeSeconds           int                    `json:"target_age_seconds"`
	ClockTickHz                uint64                 `json:"clock_tick_hz"`
	Uptime                     fixedHostFile          `json:"uptime"`
	UptimeReadStartedAt        time.Time              `json:"uptime_read_started_at"`
	UptimeReadFinishedAt       time.Time              `json:"uptime_read_finished_at"`
	EstimatedFirstProductStart time.Time              `json:"estimated_first_product_start"`
	StartEstimateUncertaintyNS int64                  `json:"start_estimate_uncertainty_ns"`
	PreparationCompletedAt     time.Time              `json:"preparation_completed_at"`
	PreparationAgeSeconds      float64                `json:"preparation_age_seconds"`
	ReleasedAt                 time.Time              `json:"released_at"`
	ReleaseAgeSeconds          float64                `json:"release_age_seconds"`
	WindowStartAgeSeconds      []float64              `json:"window_start_age_seconds"`
	WindowAgeClock             string                 `json:"window_age_clock"`
	Driver                     fixedHostProcess       `json:"driver"`
	Nodes                      []fixedHostProcess     `json:"nodes"`
	Stages                     []fixedHostStage       `json:"stages"`
	Boundaries                 []fixedHostBoundary    `json:"boundaries"`
	WindowResourceCuts         []fixedHostResourceCut `json:"window_resource_cuts"`
	ResourceCutScope           string                 `json:"resource_cut_scope"`
	firstProductStart          time.Time
	checkpoint                 func()
}

func runFixedHostStage(t *testing.T, receipt *fixedHostReceipt, name string, run func()) {
	t.Helper()
	if receipt == nil {
		run()
		return
	}
	start := time.Now()
	index := len(receipt.Stages)
	receipt.Stages = append(receipt.Stages, fixedHostStage{Name: name, StartedAt: start.UTC()})
	receipt.checkpoint()
	defer func() {
		finish := time.Now()
		receipt.Stages[index].FinishedAt = finish.UTC()
		receipt.Stages[index].DurationNS = finish.Sub(start).Nanoseconds()
		receipt.Stages[index].Failed = t.Failed()
		if t.Failed() && receipt.Failure == "" {
			receipt.Failure = name + " failed"
		}
		receipt.checkpoint()
	}()
	run()
}

func readFixedHostFile(path string) fixedHostFile {
	result := fixedHostFile{Path: path}
	file, err := os.Open(path)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		result.Error = "read failed or exceeded 1 MiB"
		return result
	}
	result.Content = string(data)
	return result
}

func observeFixedHostProcess(pid int) (fixedHostProcess, error) {
	result := fixedHostProcess{PID: pid}
	base := fmt.Sprintf("/proc/%d", pid)
	result.Stat = readFixedHostFile(filepath.Join(base, "stat"))
	result.Status = readFixedHostFile(filepath.Join(base, "status"))
	result.Membership = readFixedHostFile(filepath.Join(base, "cgroup"))
	result.MountInfo = readFixedHostFile(filepath.Join(base, "mountinfo"))
	var err error
	result.StartTicks, result.Affinity, err = parseFixedHostProcess(result.Stat.Content, result.Status.Content)
	if err != nil {
		return result, err
	}
	result.Cgroup.Location, err = resolveFixedHostCgroup(result.Membership.Content, result.MountInfo.Content)
	if err != nil {
		return result, err
	}
	result.Cgroup.Scope = "Visible process cgroup-v2 mount namespace; inaccessible host ancestors are not inferred."
	result.Cgroup.Controls = map[string]map[string]fixedHostFile{}
	location := result.Cgroup.Location
	for directory, depth := location.Directory, 0; ; directory, depth = filepath.Dir(directory), depth+1 {
		if depth >= 32 {
			return result, fmt.Errorf("cgroup ancestry exceeded 32 visible levels")
		}
		files := map[string]fixedHostFile{}
		for _, name := range []string{"cpu.max", "cpu.stat", "cpu.pressure", "cpuset.cpus.effective", "cpuset.mems.effective", "memory.max", "memory.high", "memory.current", "memory.swap.max", "memory.events", "memory.pressure", "io.stat", "io.pressure"} {
			files[name] = readFixedHostFile(filepath.Join(directory, name))
		}
		result.Cgroup.Controls[directory] = files
		if directory == location.MountPoint {
			break
		}
	}
	return result, nil
}

// captureFixedHostResources surrounds the unchanged window helper, so its
// deltas include warmup and observation overhead as well as the 60-second load.
// Actual memberships are re-resolved to detect a moved process or changed mask.
func captureFixedHostResources(t *testing.T, receipt *fixedHostReceipt, name string) {
	t.Helper()
	if receipt == nil {
		return
	}
	index := len(receipt.WindowResourceCuts)
	receipt.WindowResourceCuts = append(receipt.WindowResourceCuts, fixedHostResourceCut{Name: name, StartedAt: time.Now().UTC()})
	receipt.checkpoint()
	defer func() {
		receipt.WindowResourceCuts[index].FinishedAt = time.Now().UTC()
		receipt.checkpoint()
	}()
	processes := append([]fixedHostProcess{receipt.Driver}, receipt.Nodes...)
	for _, original := range processes {
		current, err := observeFixedHostProcess(original.PID)
		receipt.WindowResourceCuts[index].Processes = append(receipt.WindowResourceCuts[index].Processes, current)
		require.NoError(t, err)
		require.Equal(t, original.StartTicks, current.StartTicks, "fixed-host product/driver process changed")
		require.Equal(t, original.Affinity, current.Affinity, "fixed-host CPU assignment changed")
		require.Equal(t, original.Cgroup.Location, current.Cgroup.Location, "fixed-host cgroup assignment changed")
	}
}

func initializeFixedHost(t *testing.T, receipt *fixedHostReceipt) {
	t.Helper()
	if receipt == nil {
		return
	}
	require.Equal(t, "linux", runtime.GOOS)
	require.Equal(t, "amd64", runtime.GOARCH)
	require.Equal(t, 4, runtime.NumCPU())
	require.Equal(t, 4, runtime.GOMAXPROCS(0), "fixed-host driver requires GOMAXPROCS=4")
	require.NotEmpty(t, os.Getenv("WK_E2E_BINARY"), "fixed-host mode requires a prebuilt product")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "getconf", "CLK_TCK").Output()
	require.NoError(t, err, "fixed-host CLK_TCK unavailable")
	receipt.ClockTickHz, err = strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	require.NoError(t, err, "fixed-host CLK_TCK invalid")
	require.NotZero(t, receipt.ClockTickHz, "fixed-host CLK_TCK must be positive")
	receipt.Driver, err = observeFixedHostProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, validateFixedHostAffinity(receipt.Driver.Affinity))
	receipt.Driver.BinaryPath, err = os.Executable()
	require.NoError(t, err)
	binary, err := os.ReadFile(receipt.Driver.BinaryPath)
	require.NoError(t, err)
	receipt.Driver.BinarySHA256 = fmt.Sprintf("%x", sha256.Sum256(binary))
}

func bindFixedHostProducts(t *testing.T, receipt *fixedHostReceipt, cluster *suite.StartedCluster) {
	t.Helper()
	if receipt == nil {
		return
	}
	var earliest uint64
	for _, node := range cluster.Nodes {
		observation, err := observeFixedHostProcess(node.Process.Cmd.Process.Pid)
		// Preserve partial process evidence even when a required identity is absent.
		receipt.Nodes = append(receipt.Nodes, observation)
		require.NoError(t, err)
		target := &receipt.Nodes[len(receipt.Nodes)-1]
		require.NoError(t, validateFixedHostAffinity(target.Affinity))
		require.Equal(t, receipt.Driver.Affinity, target.Affinity, "all diagnostic processes must share the same four assigned CPUs")
		target.BinaryPath = node.Process.BinaryPath
		target.ConfigPath = node.Spec.ConfigPath
		raw, err := os.ReadFile(target.ConfigPath)
		require.NoError(t, err)
		target.ConfigSHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
		target.RedactedConfig, err = redactFixedHostTOML(raw)
		require.NoError(t, err)
		if earliest == 0 || target.StartTicks < earliest {
			earliest = target.StartTicks
		}
	}
	before := time.Now()
	receipt.Uptime = readFixedHostFile("/proc/uptime")
	after := time.Now()
	receipt.UptimeReadStartedAt, receipt.UptimeReadFinishedAt = before.UTC(), after.UTC()
	require.Empty(t, receipt.Uptime.Error)
	start, uncertainty, err := fixedHostStartEstimate(before, after, receipt.Uptime.Content, receipt.ClockTickHz, earliest)
	require.NoError(t, err)
	receipt.firstProductStart = start
	receipt.EstimatedFirstProductStart = start.UTC()
	receipt.StartEstimateUncertaintyNS = uncertainty.Nanoseconds()
	for i := range receipt.Nodes {
		nodeStart, _, err := fixedHostStartEstimate(before, after, receipt.Uptime.Content, receipt.ClockTickHz, receipt.Nodes[i].StartTicks)
		require.NoError(t, err)
		receipt.Nodes[i].EstimatedStart = nodeStart.UTC()
	}
}

// captureFixedHostBoundary adds one bounded cut per preparation boundary, never
// a continuous scraper. The existing suite parser binds these selected original
// samples to the same complete HTTP body's hash and time receipt.
func captureFixedHostBoundary(t *testing.T, receipt *fixedHostReceipt, cluster *suite.StartedCluster, name string) {
	t.Helper()
	if receipt == nil {
		return
	}
	cut := fixedHostBoundary{Name: name, StartedAt: time.Now().UTC()}
	index := len(receipt.Boundaries)
	receipt.Boundaries = append(receipt.Boundaries, cut)
	receipt.checkpoint()
	defer func() {
		receipt.Boundaries[index].FinishedAt = time.Now().UTC()
		receipt.checkpoint()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, node := range cluster.Nodes {
		samples, scrape, err := suite.FetchMetricSamplesWithReceipt(ctx, node.APIAddr())
		metric := fixedHostMetricCut{NodeID: node.Spec.ID, Scrape: scrape}
		if err != nil {
			metric.Error = err.Error()
		} else {
			for _, sample := range samples {
				if strings.HasPrefix(sample.Name, "go_memstats_") ||
					strings.HasPrefix(sample.Name, "go_gc_") ||
					strings.HasPrefix(sample.Name, "wukongim_storage_") ||
					sample.Name == "process_cpu_seconds_total" ||
					sample.Name == "wukongim_channelv2_runtime_load_total" ||
					sample.Name == "wukongim_channelv2_active_runtimes" ||
					sample.Name == "wukongim_conversation_membership_mutation_rows_total" {
					metric.Samples = append(metric.Samples, sample)
				}
			}
		}
		receipt.Boundaries[index].Metrics = append(receipt.Boundaries[index].Metrics, metric)
		require.NoError(t, err)
	}
}

func releaseFixedHostPreparation(t *testing.T, receipt *fixedHostReceipt) {
	t.Helper()
	if receipt == nil {
		return
	}
	prepared := time.Now()
	receipt.PreparationCompletedAt = prepared.UTC()
	receipt.PreparationAgeSeconds = prepared.Sub(receipt.firstProductStart).Seconds()
	wait, err := fixedHostPreparationWait(receipt.firstProductStart, prepared)
	if err != nil {
		receipt.Failure = err.Error()
	}
	receipt.checkpoint()
	require.NoError(t, err)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	<-timer.C
	released := time.Now()
	receipt.ReleasedAt = released.UTC()
	receipt.ReleaseAgeSeconds = released.Sub(receipt.firstProductStart).Seconds()
	receipt.Valid = true
}

func saveFixedHostReceipt(path string, receipt *fixedHostReceipt) error {
	if receipt == nil {
		return nil
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	// A test timeout may bypass defers; retain the last complete checkpoint.
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
