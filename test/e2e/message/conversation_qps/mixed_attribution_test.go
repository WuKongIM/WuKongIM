//go:build e2e

package conversation_qps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

// TestConversationQPSMixedAttribution preserves rejected fixed-load windows
// before collecting profiles separately; it cannot qualify a publication.
func TestConversationQPSMixedAttribution(t *testing.T) {
	if os.Getenv("WK_E2E_CONVERSATION_MIXED_ATTRIBUTION") != "1" {
		t.Skip("explicit four-CPU Linux AMD64 mixed attribution")
	}
	fixedHost := os.Getenv("WK_E2E_CONVERSATION_FIXED_HOST") == "1"
	if !fixedHost {
		require.Equal(t, "linux", runtime.GOOS)
		require.Equal(t, "amd64", runtime.GOARCH)
		require.Equal(t, 4, runtime.NumCPU())
	}
	output := os.Getenv("WK_E2E_CONVERSATION_MIXED_REPORT")
	require.NotEmpty(t, output)
	dir := filepath.Dir(output)
	require.NoError(t, os.MkdirAll(dir, 0755))
	p, raw, err := loadProfile()
	require.NoError(t, err)
	p.captureTimeline = true
	sha, err := exec.Command("git", "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	dirty, err := exec.Command("git", "status", "--porcelain").Output()
	require.NoError(t, err)
	digest := sha256.Sum256(raw)
	r := struct {
		Schema           string              `json:"schema"`
		HarnessSHA       string              `json:"harness_sha"`
		HarnessDirty     bool                `json:"harness_dirty"`
		ProductSHA       string              `json:"product_sha"`
		BinarySHA        string              `json:"binary_sha256"`
		ProfileSHA       string              `json:"profile_sha256"`
		DriverGOMAXPROCS int                 `json:"driver_gomaxprocs"`
		NodeGOMAXPROCS   int                 `json:"node_gomaxprocs"`
		Config           stressConfig        `json:"config"`
		Windows          []stressWindow      `json:"windows"`
		Host             []map[string]string `json:"host_snapshots"`
		ProfileWindow    *stressWindow       `json:"profile_window,omitempty"`
		FixedEnvironment *fixedHostReceipt   `json:"fixed_environment,omitempty"`
		Complete         bool                `json:"complete"`
	}{Schema: "wukongim/conversation-mixed-attribution/v1", HarnessSHA: strings.TrimSpace(string(sha)), HarnessDirty: len(dirty) > 0, ProductSHA: os.Getenv("WK_E2E_PRODUCT_SHA"), ProfileSHA: hex.EncodeToString(digest[:]), DriverGOMAXPROCS: runtime.GOMAXPROCS(0), NodeGOMAXPROCS: 2, Config: p.StressGate}
	if fixedHost {
		r.FixedEnvironment = &fixedHostReceipt{
			Schema: "wukongim/conversation-fixed-host/v1", TargetAgeSeconds: 180,
			WindowAgeClock:   "Window UTC start minus estimated UTC process start; preparation wait and release use the monotonic clock.",
			ResourceCutScope: "Cuts surround the original measurement helper, including its warmup and metrics/snapshot overhead; they are not exact 60-second counter deltas.",
		}
	}
	require.Regexp(t, `^[0-9a-f]{40}$`, r.ProductSHA)
	save := func() {
		data, e := json.MarshalIndent(r, "", "  ")
		require.NoError(t, e)
		require.NoError(t, os.WriteFile(output, append(data, '\n'), 0644))
		require.NoError(t, saveFixedHostReceipt(filepath.Join(dir, "fixed-environment.json"), r.FixedEnvironment))
	}
	if r.FixedEnvironment != nil {
		r.FixedEnvironment.checkpoint = save
	}
	defer func() {
		if r.FixedEnvironment != nil && t.Failed() {
			r.FixedEnvironment.Valid = false
			if r.FixedEnvironment.Failure == "" {
				r.FixedEnvironment.Failure = "fixed-host diagnostic aborted; inspect retained stages and test failure"
			}
		}
		save()
	}()
	runFixedHostStage(t, r.FixedEnvironment, "resource_preflight", func() {
		initializeFixedHost(t, r.FixedEnvironment)
	})
	opts := []suite.Option{suite.WithManagerHTTP()}
	for i := 1; i <= 3; i++ {
		opts = append(opts, suite.WithNodeConfigOverrides(uint64(i), map[string]string{"WK_DEBUG_API_ENABLE": "true", "WK_BENCH_API_ENABLE": "true", "WK_BENCH_API_TOKEN": "conversation-qps-fixture", "WK_BENCH_API_MAX_BATCH_SIZE": "600", "WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_CLUSTER_CHANNEL_REPLICA_N": "3"}), suite.WithNodeEnv(uint64(i), "GOMAXPROCS=2"))
	}
	var c *suite.StartedCluster
	runFixedHostStage(t, r.FixedEnvironment, "startup", func() {
		c = suite.New(t).StartStaticCluster(3, opts...)
	})
	runFixedHostStage(t, r.FixedEnvironment, "process_identity", func() {
		bindFixedHostProducts(t, r.FixedEnvironment, c)
	})
	binary, err := os.ReadFile(c.Nodes[0].Process.BinaryPath)
	require.NoError(t, err)
	digest = sha256.Sum256(binary)
	r.BinarySHA = hex.EncodeToString(digest[:])
	if r.FixedEnvironment != nil {
		for i := range r.FixedEnvironment.Nodes {
			r.FixedEnvironment.Nodes[i].BinarySHA256 = r.BinarySHA
		}
	}
	save()
	runFixedHostStage(t, r.FixedEnvironment, "readiness", func() { ready(t, c) })
	captureFixedHostBoundary(t, r.FixedEnvironment, c, "after_readiness_before_prepare")
	var expected expectedMessages
	runFixedHostStage(t, r.FixedEnvironment, "prepare", func() { expected = prepare(t, c, p) })
	captureFixedHostBoundary(t, r.FixedEnvironment, c, "after_prepare_before_eviction")
	client := &http.Client{Transport: &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 16, MaxConnsPerHost: 16}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	var initial metricsSnapshot
	runFixedHostStage(t, r.FixedEnvironment, "eviction", func() {
		initial = evictFixtureRuntimes(t, c, client, p)
	})
	captureFixedHostBoundary(t, r.FixedEnvironment, c, "after_eviction_before_age_wait")
	runFixedHostStage(t, r.FixedEnvironment, "preparation_age_wait", func() {
		releaseFixedHostPreparation(t, r.FixedEnvironment)
	})
	captureFixedHostBoundary(t, r.FixedEnvironment, c, "after_age_wait_before_original_warmup")
	if r.FixedEnvironment != nil {
		save()
	}
	for i := 0; i < 3; i++ {
		r.Host = append(r.Host, mixedHostSnapshot(t))
		captureFixedHostResources(t, r.FixedEnvironment, fmt.Sprintf("window_%d_before_warmup", i+1))
		w := measureMixed(t, c, client, p, p.StressGate, expected, initial)
		if r.FixedEnvironment != nil {
			r.FixedEnvironment.WindowStartAgeSeconds = append(r.FixedEnvironment.WindowStartAgeSeconds, w.Start.Sub(r.FixedEnvironment.EstimatedFirstProductStart).Seconds())
		}
		r.Windows = append(r.Windows, w)
		r.Host = append(r.Host, mixedHostSnapshot(t))
		captureFixedHostResources(t, r.FixedEnvironment, fmt.Sprintf("window_%d_after_measurement", i+1))
		save()
		checkStressWindow(t, w, false)
	}
	// CPU sampling and allocation snapshots are excluded from all three windows.
	fetch := func(addr, route, name string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/debug/pprof/"+route, nil)
		if e != nil {
			return e
		}
		req.Header.Set("Authorization", "Bearer conversation-qps-fixture")
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("profile HTTP %d", resp.StatusCode)
		}
		data, e := io.ReadAll(io.LimitReader(resp.Body, (32<<20)+1))
		if e != nil {
			return e
		}
		if len(data) > 32<<20 {
			return fmt.Errorf("profile exceeds 32 MiB")
		}
		return os.WriteFile(filepath.Join(dir, name), data, 0644)
	}
	for i, n := range c.Nodes {
		require.NoError(t, fetch(n.APIAddr(), "allocs", fmt.Sprintf("node%d-alloc-before.pprof", i+1)))
	}
	f, err := os.Create(filepath.Join(dir, "driver-cpu.pprof"))
	require.NoError(t, err)
	require.NoError(t, pprof.StartCPUProfile(f))
	defer f.Close()
	defer pprof.StopCPUProfile()
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i, n := range c.Nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- fetch(n.APIAddr(), "profile?seconds=8", fmt.Sprintf("node%d-cpu.pprof", i+1))
		}()
	}
	cfg := p.StressGate
	cfg.MixedSeconds = 10
	w := measureMixed(t, c, client, p, cfg, expected, initial)
	r.ProfileWindow = &w
	pprof.StopCPUProfile()
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	checkStressWindow(t, w, false)
	for i, n := range c.Nodes {
		require.NoError(t, fetch(n.APIAddr(), "allocs", fmt.Sprintf("node%d-alloc-after.pprof", i+1)))
	}
	r.Complete = true
}

// mixedHostSnapshot retains bounded raw host and driver counters, including
// CPU steal/pressure and cgroup throttling, without changing scheduling policy.
func mixedHostSnapshot(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{"time": time.Now().UTC().Format(time.RFC3339Nano)}
	for _, path := range []string{"/proc/cpuinfo", "/proc/stat", "/proc/self/stat", "/proc/self/status", "/proc/self/cgroup", "/proc/pressure/cpu", "/proc/pressure/io", "/proc/meminfo", "/sys/fs/cgroup/cpu.stat", "/sys/fs/cgroup/cpu.max"} {
		data, err := os.ReadFile(path)
		if err != nil {
			out[path] = "unavailable: " + err.Error()
			continue
		}
		require.LessOrEqual(t, len(data), 1<<20)
		out[path] = string(data)
	}
	return out
}
