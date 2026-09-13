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

type capacityCase struct {
	// FineAttempts keep the optional 400/420/440 sync probes separate from the
	// original staircase and its equal-load confirmation.
	FineAttempts     []phaseResult `json:"fine_attempts,omitempty"`
	FineConfirmedQPS int           `json:"fine_confirmed_qps,omitempty"`
	// ConfirmationSeconds distinguishes the normal short diagnostic from an opt-in sustained confirmation.
	ConfirmationSeconds int           `json:"confirmation_seconds,omitempty"`
	Nodes               int           `json:"nodes"`
	Endpoint            string        `json:"endpoint"`
	PageSize            int           `json:"page_size"`
	Workers             int           `json:"workers"`
	ConfirmedQPS        int           `json:"confirmed_qps"`
	FirstRejectedQPS    int           `json:"first_rejected_qps,omitempty"`
	Attempts            []phaseResult `json:"attempts"`
	Profiles            []string      `json:"profiles,omitempty"`
}

// TestConversationQPSCapacity explores a bounded load staircase independently of
// publication. A rejected offered rate is evidence, never a release-gate pass.
func TestConversationQPSCapacity(t *testing.T) {
	if os.Getenv("WK_E2E_CONVERSATION_CAPACITY") != "1" {
		t.Skip("explicit local capacity diagnostic")
	}
	output := os.Getenv("WK_E2E_CONVERSATION_CAPACITY_REPORT")
	require.NotEmpty(t, output)
	require.NoError(t, os.MkdirAll(filepath.Dir(output), 0755))
	p, raw, err := loadProfile()
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	sha, err := exec.Command("git", "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	dirty, err := exec.Command("git", "status", "--porcelain").Output()
	require.NoError(t, err)
	r := struct {
		Schema         string         `json:"schema"`
		SourceSHA      string         `json:"source_sha"`
		SourceDirty    bool           `json:"source_dirty"`
		ProfileSHA     string         `json:"profile_sha256"`
		BinarySHA      string         `json:"binary_sha256"`
		OS             string         `json:"os"`
		Arch           string         `json:"arch"`
		CPUs           int            `json:"cpus"`
		NodeGOMAXPROCS int            `json:"node_gomaxprocs"`
		StartedAt      time.Time      `json:"started_at"`
		Cases          []capacityCase `json:"cases"`
		Complete       bool           `json:"complete"`
	}{Schema: "wukongim/conversation-capacity/v1", SourceSHA: strings.TrimSpace(string(sha)), SourceDirty: len(dirty) > 0, ProfileSHA: hex.EncodeToString(sum[:]), OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), NodeGOMAXPROCS: 2, StartedAt: time.Now().UTC()}
	save := func() {
		data, e := json.MarshalIndent(r, "", "  ")
		require.NoError(t, e)
		require.NoError(t, os.WriteFile(output, append(data, '\n'), 0644))
	}
	defer save()
	for _, count := range []int{1, 3} {
		if !t.Run(fmt.Sprintf("%d_node_cluster", count), func(t *testing.T) {
			opts := []suite.Option{suite.WithManagerHTTP()}
			for i := 1; i <= count; i++ {
				opts = append(opts, suite.WithNodeConfigOverrides(uint64(i), map[string]string{"WK_DEBUG_API_ENABLE": "true", "WK_BENCH_API_ENABLE": "true", "WK_BENCH_API_TOKEN": "conversation-qps-fixture", "WK_BENCH_API_MAX_BATCH_SIZE": "600", "WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_CLUSTER_CHANNEL_REPLICA_N": fmt.Sprint(count)}), suite.WithNodeEnv(uint64(i), "GOMAXPROCS=2"))
			}
			cluster := suite.New(t).StartStaticCluster(count, opts...)
			binary, e := os.ReadFile(cluster.Nodes[0].Process.BinaryPath)
			require.NoError(t, e)
			digest := sha256.Sum256(binary)
			r.BinarySHA = hex.EncodeToString(digest[:])
			save()
			ready(t, cluster)
			expected := prepare(t, cluster, p)
			client := &http.Client{Transport: &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 16, MaxConnsPerHost: 16}, Timeout: 5 * time.Second}
			defer client.CloseIdleConnections()
			initial := evictFixtureRuntimes(t, cluster, client, p)
			p.Workers = 16 * count
			for _, w := range p.Cases {
				if w.PageSize != 100 {
					continue
				}
				result := measureCapacityCase(t, cluster, client, p, w, expected, initial, filepath.Dir(output))
				r.Cases = append(r.Cases, result)
				save()
			}
		}) {
			break
		}
	}
	r.Complete = !t.Failed() && len(r.Cases) == 4
}

// measureCapacityCase reuses a prepared cold fixture; optional profiles run separately.
func measureCapacityCase(t *testing.T, cluster *suite.StartedCluster, client *http.Client, p profile, w workloadCase, expected expectedMessages, initial metricsSnapshot, profileDir string) capacityCase {
	t.Helper()
	count := len(cluster.Nodes)
	p.Workers = 16 * count
	// Capacity probes enforce throughput/latency; publication allocation ceilings are separate.
	w.MaxAllocatedBytesPerRequest = nil
	result := capacityCase{Nodes: count, Endpoint: w.Endpoint, PageSize: w.PageSize, Workers: p.Workers}
	for u := 0; u < p.Cohorts*p.UsersPerCohort; u++ {
		require.NoError(t, request(context.Background(), client, cluster.Nodes[u%count].APIAddr(), p, w, u, expected))
	}
	attempt := func(qps, seconds int) bool {
		w.OfferedQPS = qps
		p.DurationSeconds = seconds
		before := snapshot(t, cluster)
		require.Equal(t, initial.loads, before.loads)
		require.Equal(t, initial.writes, before.writes)
		require.Zero(t, before.active)
		phase := measure(cluster, client, p, w, expected)
		after := snapshot(t, cluster)
		phase.RuntimeLoads = after.loads - before.loads
		phase.MembershipWrites = after.writes - before.writes
		phase.ActiveRuntimesBefore = before.active
		phase.ActiveRuntimesAfter = after.active
		phase.AllocatedBytes = after.allocated - before.allocated
		phase.HeapBytes = after.heap
		if before.cpuAvailable && after.cpuAvailable {
			v := after.cpu - before.cpu
			phase.CPUSeconds = &v
		}
		require.Zero(t, phase.RuntimeLoads)
		require.Zero(t, phase.MembershipWrites)
		require.Zero(t, phase.ActiveRuntimesAfter)
		// Queue drops, latency and recognized refusal envelopes reject this rate.
		// Other failures remain fatal, including corrupt successful pages.
		require.Zero(t, phase.UnexpectedErrors, "unexpected request failure: %s; samples=%v", phase.FirstUnexpectedError, phase.ErrorSamples)
		verdict := evaluate(p, phase)
		phase.Verdict = "pass"
		if verdict != nil {
			phase.Verdict = verdict.Error()
		}
		result.Attempts = append(result.Attempts, phase)
		t.Logf("CAPACITY nodes=%d %s page=%d offered=%d actual=%.1f p99=%.1fms drops=%d alloc/request=%.0f verdict=%s", count, w.Endpoint, w.PageSize, qps, phase.ActualQPS, phase.P99MS, phase.Dropped, phase.AllocatedBytes/float64(max(1, phase.Completed)), phase.Verdict)
		return verdict == nil
	}
	low, high := 0, 0
	for q, step := w.OfferedQPS, 0; step < 7; step, q = step+1, q*2 {
		if attempt(q, 10) {
			low = q
		} else {
			high = q
			break
		}
	}
	if high > 0 && low > 0 {
		mid := (low + high) / 2
		if attempt(mid, 10) {
			low = mid
		} else {
			high = mid
		}
	}
	require.Positive(t, low, "no passing offered rate")
	// A short probe alone is insufficient. Optional sustained confirmation is
	// diagnostic only; publication thresholds and its fixed phase durations stay unchanged.
	confirmationSeconds := 15
	if os.Getenv("WK_E2E_CONVERSATION_CAPACITY_LONG_CONFIRM") == "1" {
		confirmationSeconds = 180
	}
	result.ConfirmationSeconds = confirmationSeconds
	for !attempt(low, confirmationSeconds) {
		high = low
		low /= 2
		require.Positive(t, low)
	}
	result.ConfirmedQPS = low
	result.FirstRejectedQPS = high
	if os.Getenv("WK_E2E_CONVERSATION_CAPACITY_FINE_SYNC") == "1" && count == 3 && w.Endpoint == "/conversation/sync" && w.PageSize == 100 {
		// Preserve every rejected probe. Only an unprofiled three-minute pass
		// confirms a finer rate; this cannot alter publication gate thresholds.
		start := len(result.Attempts)
		var passing []int
		for _, qps := range []int{400, 420, 440} {
			if attempt(qps, 10) {
				passing = append(passing, qps)
			}
		}
		for i := len(passing) - 1; i >= 0; i-- {
			if attempt(passing[i], 180) {
				result.FineConfirmedQPS = passing[i]
				break
			}
		}
		result.FineAttempts = append([]phaseResult(nil), result.Attempts[start:]...)
		result.Attempts = result.Attempts[:start]
	}
	if profileDir != "" {
		w.OfferedQPS = low
		p.DurationSeconds = 10
		result.Profiles = captureCapacityProfiles(t, cluster, client, p, w, expected, profileDir)
		profileEnd := snapshot(t, cluster)
		require.Equal(t, initial.loads, profileEnd.loads)
		require.Equal(t, initial.writes, profileEnd.writes)
		require.Zero(t, profileEnd.active)
	}
	return result
}

// captureCapacityProfiles keeps profiler overhead out of reported capacity.
// Each node and the response-validating driver have separate CPU profiles.
func captureCapacityProfiles(t *testing.T, c *suite.StartedCluster, client *http.Client, p profile, w workloadCase, expected expectedMessages, dir string) []string {
	t.Helper()
	prefix := fmt.Sprintf("%dn-%s-%d", len(c.Nodes), strings.TrimPrefix(w.Endpoint, "/conversation/"), w.PageSize)
	fetch := func(addr, route, path string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+strings.TrimPrefix(addr, "http://")+"/debug/pprof/"+route, nil)
		if e != nil {
			return e
		}
		req.Header.Set("Authorization", "Bearer conversation-qps-fixture")
		profileClient := &http.Client{Timeout: 15 * time.Second}
		resp, e := profileClient.Do(req)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("profile HTTP %d", resp.StatusCode)
		}
		data, e := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
		if e != nil {
			return e
		}
		if len(data) > 32<<20 {
			return fmt.Errorf("profile exceeds bound")
		}
		return os.WriteFile(path, data, 0644)
	}
	var paths []string
	for i, node := range c.Nodes {
		path := filepath.Join(dir, fmt.Sprintf("%s-node%d-alloc-before.pprof", prefix, i+1))
		require.NoError(t, fetch(node.APIAddr(), "allocs", path))
		paths = append(paths, path)
	}
	driver := filepath.Join(dir, prefix+"-driver-cpu.pprof")
	f, e := os.Create(driver)
	require.NoError(t, e)
	require.NoError(t, pprof.StartCPUProfile(f))
	paths = append(paths, driver)
	var wg sync.WaitGroup
	errs := make(chan error, len(c.Nodes))
	for i, node := range c.Nodes {
		path := filepath.Join(dir, fmt.Sprintf("%s-node%d-cpu.pprof", prefix, i+1))
		paths = append(paths, path)
		wg.Add(1)
		go func(addr, path string) { defer wg.Done(); errs <- fetch(addr, "profile?seconds=8", path) }(node.APIAddr(), path)
	}
	phase := measure(c, client, p, w, expected)
	pprof.StopCPUProfile()
	require.NoError(t, f.Close())
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	// Profiling can perturb admission. Keep refusals visible without treating
	// this separate instrumented phase as capacity or publication evidence.
	t.Logf("PROFILE nodes=%d %s offered=%d actual=%.1f errors=%d drops=%d", len(c.Nodes), w.Endpoint, w.OfferedQPS, phase.ActualQPS, phase.Errors, phase.Dropped)
	require.Zero(t, phase.UnexpectedErrors, "profile workload: %s", phase.FirstError)
	for i, node := range c.Nodes {
		path := filepath.Join(dir, fmt.Sprintf("%s-node%d-alloc-after.pprof", prefix, i+1))
		require.NoError(t, fetch(node.APIAddr(), "allocs", path))
		paths = append(paths, path)
	}
	return paths
}
