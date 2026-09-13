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
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestConversationQPSMixedAndHiddenDiagnosis(t *testing.T) {
	if os.Getenv("WK_E2E_CONVERSATION_STRESS") != "1" {
		t.Skip("explicit bounded mixed and hidden diagnosis")
	}
	stage := os.Getenv("WK_E2E_CONVERSATION_STRESS_STAGE")
	if stage == "" {
		stage = "all"
	}
	require.Contains(t, []string{"all", "mixed", "hidden"}, stage)
	p, raw, err := loadProfile()
	require.NoError(t, err)
	output := os.Getenv("WK_E2E_CONVERSATION_STRESS_REPORT")
	require.NotEmpty(t, output)
	require.NoError(t, os.MkdirAll(filepath.Dir(output), 0755))
	sha, err := exec.Command("git", "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	dirty, err := exec.Command("git", "status", "--porcelain").Output()
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	r := stressReport{Stage: stage, Schema: "wukongim/conversation-stress/v1", SourceSHA: strings.TrimSpace(string(sha)), SourceDirty: len(dirty) > 0, ProfileSHA: hex.EncodeToString(sum[:]), OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), DriverGOMAXPROCS: runtime.GOMAXPROCS(0), NodeGOMAXPROCS: 2, StartedAt: time.Now().UTC(), Config: p.StressDiagnosis}
	save := func() {
		b, e := json.MarshalIndent(r, "", "  ")
		require.NoError(t, e)
		require.NoError(t, os.WriteFile(output, append(b, '\n'), 0644))
	}
	defer save()
	opts := []suite.Option{suite.WithManagerHTTP()}
	for i := 1; i <= 3; i++ {
		opts = append(opts, suite.WithNodeConfigOverrides(uint64(i), map[string]string{"WK_DEBUG_API_ENABLE": "true", "WK_BENCH_API_ENABLE": "true", "WK_BENCH_API_TOKEN": "conversation-qps-fixture", "WK_BENCH_API_MAX_BATCH_SIZE": "600", "WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_CLUSTER_CHANNEL_REPLICA_N": "3"}), suite.WithNodeEnv(uint64(i), "GOMAXPROCS=2"))
	}
	c := suite.New(t).StartStaticCluster(3, opts...)
	b, err := os.ReadFile(c.Nodes[0].Process.BinaryPath)
	require.NoError(t, err)
	sum = sha256.Sum256(b)
	r.BinarySHA = hex.EncodeToString(sum[:])
	save()
	ready(t, c)
	expected := prepare(t, c, p)
	client := &http.Client{Transport: &http.Transport{MaxIdleConns: 96, MaxIdleConnsPerHost: 32, MaxConnsPerHost: 32}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	if stage != "hidden" {
		initial := evictFixtureRuntimes(t, c, client, p)
		mixed := measureMixed(t, c, client, p, p.StressDiagnosis, expected, initial)
		r.Windows = append(r.Windows, mixed)
		save()
		checkStressWindow(t, mixed, false)
	}
	if stage != "mixed" {
		hideStressFixture(t, c, p)
		initial := evictFixtureRuntimes(t, c, client, p)
		for cohort := 0; cohort < 3; cohort++ {
			for _, page := range []int{1, 2} {
				window := measureHidden(t, c, client, p, p.StressDiagnosis, cohort, page, expected, initial)
				r.Windows = append(r.Windows, window)
				save()
				checkStressWindow(t, window, false)
			}
		}
	}
	want := map[string]int{"all": 7, "mixed": 1, "hidden": 6}[stage]
	r.Complete = !t.Failed() && len(r.Windows) == want

	r.Passed = r.Complete
	for _, window := range r.Windows {
		for _, phase := range window.Phases {
			if phase.Verdict != "pass" {
				r.Passed = false
			}
		}
	}
}

func beginStressWindow(t *testing.T, c *suite.StartedCluster, initial metricsSnapshot, name string) (stressWindow, metricsSnapshot) {
	t.Helper()
	before := snapshot(t, c)
	require.Equal(t, initial.loads, before.loads)
	require.Equal(t, initial.writes, before.writes)
	require.Zero(t, before.active)
	return stressWindow{Name: name, Before: diagnosticMetrics(t, c)}, before
}

func finishStressWindow(t *testing.T, c *suite.StartedCluster, w *stressWindow, before metricsSnapshot, p profile) {
	t.Helper()
	after := snapshot(t, c)
	w.After = diagnosticMetrics(t, c)
	require.True(t, before.cpuAvailable && after.cpuAvailable, "stress attribution requires CPU metrics")
	w.CPUSeconds = after.cpu - before.cpu
	w.AllocatedBytes = after.allocated - before.allocated
	w.HeapBytes = after.heap
	w.RuntimeLoads = after.loads - before.loads
	w.MembershipWrites = after.writes - before.writes
	w.ActiveBefore = before.active
	w.ActiveAfter = after.active
	for i := range w.Phases {
		phase := &w.Phases[i]
		phase.RuntimeLoads = w.RuntimeLoads
		phase.MembershipWrites = w.MembershipWrites
		phase.ActiveRuntimesBefore = w.ActiveBefore
		phase.ActiveRuntimesAfter = w.ActiveAfter
		phase.Verdict = "pass"
		if err := evaluate(p, *phase); err != nil {
			phase.Verdict = err.Error()
		}
		t.Logf("STRESS name=%s page=%d endpoint=%s offered=%d actual=%.2f p99=%.2f errors=%d drops=%d verdict=%s", w.Name, w.Page, phase.Case.Endpoint, phase.Case.OfferedQPS, phase.ActualQPS, phase.P99MS, phase.Errors, phase.Dropped, phase.Verdict)
	}
}

func checkStressWindow(t *testing.T, w stressWindow, enforce bool) {
	t.Helper()
	require.Zero(t, w.RuntimeLoads)
	require.Zero(t, w.MembershipWrites)
	require.Zero(t, w.ActiveBefore)
	require.Zero(t, w.ActiveAfter)
	for _, phase := range w.Phases {
		require.Zero(t, phase.UnexpectedErrors, "%s", phase.FirstUnexpectedError)
		if enforce {
			require.Equal(t, "pass", phase.Verdict)
		}
	}
}

func measureMixed(t *testing.T, c *suite.StartedCluster, client *http.Client, p profile, cfg stressConfig, expected expectedMessages, initial metricsSnapshot) stressWindow {
	t.Helper()
	p.DurationSeconds = cfg.MixedSeconds
	p.Workers = cfg.MixedWorkersPerEndpoint
	cases := []workloadCase{{Endpoint: "/conversation/list", PageSize: 100, OfferedQPS: cfg.MixedListQPS}, {Endpoint: "/conversation/sync", PageSize: 100, OfferedQPS: cfg.MixedSyncQPS}}
	for _, w := range cases {
		for user := 0; user < p.Cohorts*p.UsersPerCohort; user++ {
			require.NoError(t, request(context.Background(), client, c.Nodes[user%len(c.Nodes)].APIAddr(), p, w, user, expected))
		}
	}
	w, before := beginStressWindow(t, c, initial, "mixed")
	w.Phases = make([]phaseResult, len(cases))
	start := time.Now().Add(100 * time.Millisecond)
	w.Start = start.UTC()
	w.End = w.Start.Add(time.Duration(p.DurationSeconds) * time.Second)
	var group sync.WaitGroup
	for i, work := range cases {
		group.Add(1)
		go func() {
			defer group.Done()
			w.Phases[i] = measureRequests(p, work, len(c.Nodes), start, func(ctx context.Context, index int) error {
				return request(ctx, client, c.Nodes[index%len(c.Nodes)].APIAddr(), p, work, index%(p.Cohorts*p.UsersPerCohort), expected)
			})
		}()
	}
	group.Wait()
	finishStressWindow(t, c, &w, before, p)
	return w
}

func measureHidden(t *testing.T, c *suite.StartedCluster, client *http.Client, p profile, cfg stressConfig, cohort, page int, expected expectedMessages, initial metricsSnapshot) stressWindow {
	t.Helper()
	p.DurationSeconds = cfg.HiddenSeconds
	p.Workers = cfg.HiddenWorkers
	size := 100
	if page == 2 {
		size = 50
	}
	work := workloadCase{Endpoint: "/conversation/sync", PageSize: size, OfferedQPS: cfg.HiddenQPS}
	ids := visiblePage(p, cohort, page, size)
	call := func(ctx context.Context, index int) error {
		user := cohort*p.UsersPerCohort + index%p.UsersPerCohort
		body := map[string]any{"uid": fmt.Sprintf("qps-user-%d", user), "version": 0, "msg_count": p.MessagesPerChannel, "page": page, "page_size": size}
		raw, err := conversationResponse(ctx, client, c.Nodes[index%len(c.Nodes)].APIAddr(), work.Endpoint, body)
		if err != nil {
			return err
		}
		return validateHiddenSync(raw, ids, expected)
	}
	for i := 0; i < p.UsersPerCohort; i++ {
		require.NoError(t, call(context.Background(), i))
	}
	names := []string{"hidden_50_percent", "hidden_90_percent", "hidden_after_99_visible"}
	w, before := beginStressWindow(t, c, initial, names[cohort])
	w.Cohort = cohort
	w.Page = page
	start := time.Now()
	w.Start = start.UTC()
	w.End = w.Start.Add(time.Duration(p.DurationSeconds) * time.Second)
	w.Phases = []phaseResult{measureRequests(p, work, len(c.Nodes), start, call)}
	finishStressWindow(t, c, &w, before, p)
	return w
}

// hideStressFixture changes only exact fixture-owned memberships between windows.
func hideStressFixture(t *testing.T, c *suite.StartedCluster, p profile) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	type job struct {
		user    int
		channel string
	}
	jobs := make(chan job)
	errs := make(chan error, p.Cohorts*p.UsersPerCohort*p.ChannelsPerCohort)
	var workers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := range jobs {
				if ctx.Err() != nil {
					continue
				}
				_, err := suite.PostJSON(ctx, "http://"+c.Nodes[j.user%len(c.Nodes)].APIAddr()+"/conversations/delete", map[string]any{"uid": fmt.Sprintf("qps-user-%d", j.user), "channel_id": j.channel, "channel_type": 2}, nil)
				if err != nil {
					errs <- err
					cancel()
				}
			}
		}()
	}
	for cohort := 0; cohort < p.Cohorts; cohort++ {
		for rank, id := range cohortChannels(p, cohort) {
			if hiddenAt(cohort, rank) {
				for u := 0; u < p.UsersPerCohort; u++ {
					jobs <- job{user: cohort*p.UsersPerCohort + u, channel: id}
				}
			}
		}
	}
	close(jobs)
	workers.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, ctx.Err())
	t.Log("HIDDEN fixture prepared through public hide commands")
}

// runReleaseStress retains every completed window before enforcing its verdict.
func runReleaseStress(t *testing.T, c *suite.StartedCluster, client *http.Client, p profile, cfg stressConfig, expected expectedMessages, initial metricsSnapshot, record func(stressWindow)) {
	t.Helper()
	w := measureMixed(t, c, client, p, cfg, expected, initial)
	record(w)
	require.NoError(t, evaluateStressWindow(p, cfg, w))
	hideStressFixture(t, c, p)
	initial = evictFixtureRuntimes(t, c, client, p)
	for cohort := 0; cohort < 3; cohort++ {
		for _, page := range []int{1, 2} {
			w = measureHidden(t, c, client, p, cfg, cohort, page, expected, initial)
			record(w)
			require.NoError(t, evaluateStressWindow(p, cfg, w))
		}
	}
}
