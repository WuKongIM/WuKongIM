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
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

type diagnosticWindow struct {
	Phase  phaseResult          `json:"phase"`
	Before []map[string]float64 `json:"metrics_before"`
	After  []map[string]float64 `json:"metrics_after"`
}

type diagnosticCase struct {
	Capacity  capacityCase       `json:"capacity"`
	Windows   []diagnosticWindow `json:"windows"`
	Profiles  []string           `json:"profiles"`
	TraceLoad phaseResult        `json:"trace_load"`
}

// TestConversationQPSDiagnosis measures three successive one-minute windows
// near the observed capacity, then profiles separately. Refused diagnostic
// loads remain explicit evidence; only unexpected failures abort diagnosis.
func TestConversationQPSDiagnosis(t *testing.T) {
	if os.Getenv("WK_E2E_CONVERSATION_DIAGNOSIS") != "1" {
		t.Skip("explicit bounded throughput diagnosis")
	}
	output := os.Getenv("WK_E2E_CONVERSATION_DIAGNOSIS_REPORT")
	require.NotEmpty(t, output)
	require.NoError(t, os.MkdirAll(filepath.Dir(output), 0755))
	p, raw, err := loadProfile()
	require.NoError(t, err)
	sha, err := exec.Command("git", "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	dirty, err := exec.Command("git", "status", "--porcelain").Output()
	require.NoError(t, err)
	profileSHA := sha256.Sum256(raw)
	r := struct {
		FixedBaselineLoad bool              `json:"fixed_baseline_load"`
		Schema            string            `json:"schema"`
		SourceSHA         string            `json:"source_sha"`
		SourceDirty       bool              `json:"source_dirty"`
		ProfileSHA        string            `json:"profile_sha256"`
		BinarySHA         string            `json:"binary_sha256"`
		OS                string            `json:"os"`
		Arch              string            `json:"arch"`
		CPUs              int               `json:"cpus"`
		DriverGOMAXPROCS  int               `json:"driver_gomaxprocs"`
		NodeGOMAXPROCS    int               `json:"node_gomaxprocs"`
		StartedAt         time.Time         `json:"started_at"`
		Cases             []*diagnosticCase `json:"cases"`
		Complete          bool              `json:"complete"`
	}{FixedBaselineLoad: os.Getenv("WK_E2E_CONVERSATION_DIAGNOSIS_FIXED_BASELINE") == "1", Schema: "wukongim/conversation-diagnosis/v1", SourceSHA: strings.TrimSpace(string(sha)), SourceDirty: len(dirty) > 0, ProfileSHA: hex.EncodeToString(profileSHA[:]), OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), DriverGOMAXPROCS: runtime.GOMAXPROCS(0), NodeGOMAXPROCS: 2, StartedAt: time.Now().UTC()}
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
			identity := hex.EncodeToString(digest[:])
			if r.BinarySHA != "" {
				require.Equal(t, r.BinarySHA, identity)
			}
			r.BinarySHA = identity
			save()
			ready(t, cluster)
			expected := prepare(t, cluster, p)
			client := &http.Client{Transport: &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 16, MaxConnsPerHost: 16}, Timeout: 5 * time.Second}
			defer client.CloseIdleConnections()
			initial := evictFixtureRuntimes(t, cluster, client, p)
			for _, w := range p.Cases {
				if w.PageSize != 100 {
					continue
				}
				c := &diagnosticCase{}
				r.Cases = append(r.Cases, c)
				if r.FixedBaselineLoad {
					// Frozen 2026-09-12 Linux diagnostic rates, not release thresholds or new capacity claims.
					rate := 800
					if count == 3 {
						rate = 1200
					}
					if w.Endpoint == "/conversation/sync" {
						rate = 240
						if count == 3 {
							rate = 360
						}
					}
					c.Capacity = capacityCase{Nodes: count, Endpoint: w.Endpoint, PageSize: w.PageSize, Workers: 16 * count, ConfirmedQPS: rate}
				} else {
					c.Capacity = measureCapacityCase(t, cluster, client, p, w, expected, initial, "")
				}
				p.Workers = 16 * count
				p.DurationSeconds = 60
				w.OfferedQPS = c.Capacity.ConfirmedQPS
				w.MaxAllocatedBytesPerRequest = nil
				for window := 1; window <= 3; window++ {
					d := diagnosticWindow{Before: diagnosticMetrics(t, cluster)}
					before := snapshot(t, cluster)
					d.Phase = measure(cluster, client, p, w, expected)
					after := snapshot(t, cluster)
					d.After = diagnosticMetrics(t, cluster)
					d.Phase.RuntimeLoads = after.loads - before.loads
					d.Phase.MembershipWrites = after.writes - before.writes
					d.Phase.ActiveRuntimesBefore = before.active
					d.Phase.ActiveRuntimesAfter = after.active
					d.Phase.AllocatedBytes = after.allocated - before.allocated
					d.Phase.HeapBytes = after.heap
					if before.cpuAvailable && after.cpuAvailable {
						v := after.cpu - before.cpu
						d.Phase.CPUSeconds = &v
					}
					d.Phase.Verdict = "pass"
					if e := evaluate(p, d.Phase); e != nil {
						d.Phase.Verdict = e.Error()
					}
					c.Windows = append(c.Windows, d)
					save()
					t.Logf("STEADY nodes=%d endpoint=%s minute=%d offered=%d actual=%.1f p99=%.1fms errors=%d drops=%d verdict=%s", count, w.Endpoint, window, w.OfferedQPS, d.Phase.ActualQPS, d.Phase.P99MS, d.Phase.Errors, d.Phase.Dropped, d.Phase.Verdict)
					require.Zero(t, d.Phase.UnexpectedErrors, "%s", d.Phase.FirstUnexpectedError)
					require.Equal(t, initial.loads, after.loads)
					require.Equal(t, initial.writes, after.writes)
					require.Zero(t, after.active)
				}
				p.DurationSeconds = 10
				c.Profiles = captureCapacityProfiles(t, cluster, client, p, w, expected, filepath.Dir(output))
				paths, phase := captureDiagnosticTraces(t, cluster, client, p, w, expected, filepath.Dir(output))
				c.Profiles = append(c.Profiles, paths...)
				c.TraceLoad = phase
				after := snapshot(t, cluster)
				require.Equal(t, initial.loads, after.loads)
				require.Equal(t, initial.writes, after.writes)
				require.Zero(t, after.active)
				save()
			}
		}) {
			break
		}
	}
	r.Complete = !t.Failed() && len(r.Cases) == 4
}

// diagnosticMetrics records only bounded, existing metric families. Counters
// and histogram sums/counts support per-node CPU and RPC/hydration attribution.
func diagnosticMetrics(t *testing.T, c *suite.StartedCluster) []map[string]float64 {
	t.Helper()
	result := make([]map[string]float64, len(c.Nodes))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i, node := range c.Nodes {
		samples, err := suite.FetchMetricSamples(ctx, node.APIAddr())
		require.NoError(t, err)
		result[i] = map[string]float64{}
		for _, s := range samples {
			if !(strings.HasPrefix(s.Name, "wukongim_conversation_") || strings.HasPrefix(s.Name, "wukongim_transport_rpc_") || strings.HasPrefix(s.Name, "go_memstats_") || s.Name == "process_cpu_seconds_total" || s.Name == "go_gc_duration_seconds_sum" || s.Name == "go_gc_duration_seconds_count") {
				continue
			}
			if strings.HasSuffix(s.Name, "_bucket") && !strings.HasPrefix(s.Name, "wukongim_conversation_persisted_") {
				continue
			}
			var labels []string
			for k, v := range s.Labels {
				labels = append(labels, k+"="+v)
			}
			sort.Strings(labels)
			result[i][s.Name+"{"+strings.Join(labels, ",")+"}"] = s.Value
		}
	}
	return result
}

// captureDiagnosticTraces uses the existing authenticated five-second Go trace
// endpoint. Offline trace profiles separate network, syscall, sync and scheduling
// waits without changing production profiling rates or enabling new entrypoints.
func captureDiagnosticTraces(t *testing.T, c *suite.StartedCluster, client *http.Client, p profile, w workloadCase, expected expectedMessages, dir string) ([]string, phaseResult) {
	t.Helper()
	var paths []string
	var wg sync.WaitGroup
	errs := make(chan error, len(c.Nodes))
	for i, node := range c.Nodes {
		path := filepath.Join(dir, fmt.Sprintf("%dn-%s-%d-node%d.trace", len(c.Nodes), strings.TrimPrefix(w.Endpoint, "/conversation/"), w.PageSize, i+1))
		paths = append(paths, path)
		wg.Add(1)
		go func(addr, path string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+strings.TrimPrefix(addr, "http://")+"/debug/pprof/trace?seconds=5", nil)
			if err != nil {
				errs <- err
				return
			}
			req.Header.Set("Authorization", "Bearer conversation-qps-fixture")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errs <- err
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Errorf("trace HTTP %d", resp.StatusCode)
				return
			}
			data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20+1))
			if err != nil {
				errs <- err
				return
			}
			if len(data) > 64<<20 {
				errs <- fmt.Errorf("trace exceeds bound")
				return
			}
			errs <- os.WriteFile(path, data, 0644)
		}(node.APIAddr(), path)
	}
	p.DurationSeconds = 8
	phase := measure(c, client, p, w, expected)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Zero(t, phase.UnexpectedErrors, "%s", phase.FirstUnexpectedError)
	return paths, phase
}
