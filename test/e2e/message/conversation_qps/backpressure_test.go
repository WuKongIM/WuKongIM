//go:build e2e

package conversation_qps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

type backpressureCase struct {
	Workload  workloadCase       `json:"workload"`
	Windows   []diagnosticWindow `json:"windows"`
	Profiles  []string           `json:"profiles"`
	TraceLoad phaseResult        `json:"trace_load"`
}

// TestConversationQPSBackpressure compares fixed offered loads without discovery,
// halving retries or publication claims. Invoke once per exact candidate binary.
func TestConversationQPSBackpressure(t *testing.T) {
	if os.Getenv("WK_E2E_CONVERSATION_BACKPRESSURE") != "1" {
		t.Skip("explicit bounded backpressure diagnosis")
	}
	output := os.Getenv("WK_E2E_CONVERSATION_BACKPRESSURE_REPORT")
	runConversationBackpressure(t, output, false, false)
}

// TestConversationQPSPeakConfirmation records one uninterrupted 180-second
// window per fixed peak load. Run alternating exact binaries three times each.
func TestConversationQPSPeakConfirmation(t *testing.T) {
	if os.Getenv("WK_E2E_CONVERSATION_PEAK_CONFIRM") != "1" {
		t.Skip("explicit bounded peak confirmation")
	}
	runConversationBackpressure(t, os.Getenv("WK_E2E_CONVERSATION_BACKPRESSURE_REPORT"), true, false)
}

// TestConversationQPSSyncPrefixConfirmation isolates the fixed sync comparison
// while retaining the same fresh cluster, fixture, workers and 180-second window.
func TestConversationQPSSyncPrefixConfirmation(t *testing.T) {
	if os.Getenv("WK_E2E_CONVERSATION_SYNC_PREFIX_CONFIRM") != "1" {
		t.Skip("explicit bounded sync prefix confirmation")
	}
	runConversationBackpressure(t, os.Getenv("WK_E2E_CONVERSATION_BACKPRESSURE_REPORT"), true, true)
}

func runConversationBackpressure(t *testing.T, output string, longConfirmation, syncOnly bool) {
	t.Helper()
	require.NotEmpty(t, output)
	require.NoError(t, os.MkdirAll(filepath.Dir(output), 0755))
	p, raw, err := loadProfile()
	require.NoError(t, err)
	sha, err := exec.Command("git", "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	dirty, err := exec.Command("git", "status", "--porcelain").Output()
	require.NoError(t, err)
	digest := sha256.Sum256(raw)
	r := struct {
		SyncOnly         bool                `json:"sync_only"`
		LongConfirmation bool                `json:"long_confirmation"`
		Schema           string              `json:"schema"`
		SourceBase       string              `json:"source_base"`
		SourceDirty      bool                `json:"source_dirty"`
		ProfileSHA       string              `json:"profile_sha256"`
		BinarySHA        string              `json:"binary_sha256"`
		OS               string              `json:"os"`
		Arch             string              `json:"arch"`
		CPUs             int                 `json:"cpus"`
		DriverGOMAXPROCS int                 `json:"driver_gomaxprocs"`
		NodeGOMAXPROCS   int                 `json:"node_gomaxprocs"`
		StartedAt        time.Time           `json:"started_at"`
		Cases            []*backpressureCase `json:"cases"`
		Complete         bool                `json:"complete"`
	}{SyncOnly: syncOnly, LongConfirmation: longConfirmation, Schema: "wukongim/conversation-backpressure/v1", SourceBase: strings.TrimSpace(string(sha)), SourceDirty: len(dirty) > 0, ProfileSHA: hex.EncodeToString(digest[:]), OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), DriverGOMAXPROCS: runtime.GOMAXPROCS(0), NodeGOMAXPROCS: 2, StartedAt: time.Now().UTC()}
	save := func() {
		data, e := json.MarshalIndent(r, "", "  ")
		require.NoError(t, e)
		require.NoError(t, os.WriteFile(output, append(data, '\n'), 0644))
	}
	defer save()
	opts := []suite.Option{suite.WithManagerHTTP()}
	for i := 1; i <= 3; i++ {
		opts = append(opts, suite.WithNodeConfigOverrides(uint64(i), map[string]string{"WK_DEBUG_API_ENABLE": "true", "WK_BENCH_API_ENABLE": "true", "WK_BENCH_API_TOKEN": "conversation-qps-fixture", "WK_BENCH_API_MAX_BATCH_SIZE": "600", "WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_CLUSTER_CHANNEL_REPLICA_N": "3"}), suite.WithNodeEnv(uint64(i), "GOMAXPROCS=2"))
	}
	cluster := suite.New(t).StartStaticCluster(3, opts...)
	binary, e := os.ReadFile(cluster.Nodes[0].Process.BinaryPath)
	require.NoError(t, e)
	digest = sha256.Sum256(binary)
	r.BinarySHA = hex.EncodeToString(digest[:])
	save()
	ready(t, cluster)
	expected := prepare(t, cluster, p)
	client := &http.Client{Transport: &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 16, MaxConnsPerHost: 16}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	initial := evictFixtureRuntimes(t, cluster, client, p)
	p.Workers = 48
	workloads := []workloadCase{{Endpoint: "/conversation/list", PageSize: 100, OfferedQPS: 1200}, {Endpoint: "/conversation/sync", PageSize: 100, OfferedQPS: 400}, {Endpoint: "/conversation/sync", PageSize: 100, OfferedQPS: 420}}
	windows, seconds := 3, 60
	if longConfirmation {
		workloads = []workloadCase{workloads[0], workloads[2]}
		windows, seconds = 1, 180
	}
	if syncOnly {
		workloads = workloads[len(workloads)-1:]
	}
	for _, w := range workloads {
		c := &backpressureCase{Workload: w}
		r.Cases = append(r.Cases, c)
		save()
		for u := 0; u < p.Cohorts*p.UsersPerCohort; u++ {
			require.NoError(t, request(context.Background(), client, cluster.Nodes[u%3].APIAddr(), p, w, u, expected))
		}
		for window := 1; window <= windows; window++ {
			p.DurationSeconds = seconds
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
			require.True(t, before.cpuAvailable && after.cpuAvailable, "backpressure diagnosis requires process CPU metrics")
			cpu := after.cpu - before.cpu
			d.Phase.CPUSeconds = &cpu
			d.Phase.Verdict = "pass"
			if e := evaluate(p, d.Phase); e != nil {
				d.Phase.Verdict = e.Error()
			}
			c.Windows = append(c.Windows, d)
			save()
			t.Logf("PRESSURE endpoint=%s window=%d seconds=%d offered=%d actual=%.1f p99=%.1fms driver_wait_p99=%.1fms errors=%d drops=%d verdict=%s", w.Endpoint, window, seconds, w.OfferedQPS, d.Phase.ActualQPS, d.Phase.P99MS, d.Phase.DriverWaitP99MS, d.Phase.Errors, d.Phase.Dropped, d.Phase.Verdict)
			require.Zero(t, d.Phase.UnexpectedErrors, "%s", d.Phase.FirstUnexpectedError)
			require.Equal(t, initial.loads, after.loads)
			require.Equal(t, initial.writes, after.writes)
			require.Zero(t, after.active)
		}
		if longConfirmation {
			continue
		}
		dir := filepath.Join(filepath.Dir(output), fmt.Sprintf("%s-%d", strings.TrimPrefix(w.Endpoint, "/conversation/"), w.OfferedQPS))
		require.NoError(t, os.MkdirAll(dir, 0755))
		p.DurationSeconds = 10
		c.Profiles = captureCapacityProfiles(t, cluster, client, p, w, expected, dir)
		paths, phase := captureDiagnosticTraces(t, cluster, client, p, w, expected, dir)
		c.Profiles = append(c.Profiles, paths...)
		c.TraceLoad = phase
		after := snapshot(t, cluster)
		require.Equal(t, initial.loads, after.loads)
		require.Equal(t, initial.writes, after.writes)
		require.Zero(t, after.active)
		save()
	}
	r.Complete = !t.Failed() && len(r.Cases) == len(workloads)
}
