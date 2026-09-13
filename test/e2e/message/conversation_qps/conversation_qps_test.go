//go:build e2e

package conversation_qps

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// report binds the complete matrix to source, profile and the actual server binary.
// Dirty-tree runs provide local diagnostics, never publication evidence.
type report struct {
	Schema         string         `json:"schema"`
	SourceSHA      string         `json:"source_sha"`
	SourceDirty    bool           `json:"source_dirty"`
	ProfileSHA256  string         `json:"profile_sha256"`
	BinarySHA256   string         `json:"binary_sha256"`
	OS             string         `json:"os"`
	Arch           string         `json:"arch"`
	CPUs           int            `json:"cpus"`
	NodeGOMAXPROCS int            `json:"node_gomaxprocs"`
	StartedAt      time.Time      `json:"started_at"`
	StressConfig   stressConfig   `json:"stress_config"`
	Stress         []stressWindow `json:"stress"`
	Capacity       []capacityCase `json:"capacity,omitempty"`
	Phases         []phaseResult  `json:"phases"`
	Passed         bool           `json:"passed"`
}

func TestConversationQPSReleaseGate(t *testing.T) {
	if os.Getenv("WK_E2E_CONVERSATION_QPS") != "1" {
		t.Skip("explicit bounded QPS gate")
	}
	withCapacity := os.Getenv("WK_E2E_CONVERSATION_CAPACITY_WITH_GATE") == "1"
	p, raw, err := loadProfile()
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	sha, err := exec.Command("git", "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	dirty, err := exec.Command("git", "status", "--porcelain", "--untracked-files=normal").Output()
	require.NoError(t, err)
	r := report{StressConfig: p.StressGate, Schema: "wukongim/conversation-qps-report/v2", SourceSHA: strings.TrimSpace(string(sha)), SourceDirty: len(dirty) > 0, ProfileSHA256: hex.EncodeToString(sum[:]), OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), NodeGOMAXPROCS: 2, StartedAt: time.Now().UTC()}
	output := os.Getenv("WK_E2E_CONVERSATION_QPS_REPORT")
	require.NotEmpty(t, output, "report path is required; absence must not yield a release pass")
	defer func() {
		r.Passed = !t.Failed() && len(r.Phases) == 12 && len(r.Stress) == 7 && (!withCapacity || len(r.Capacity) == 4)
		data, e := json.MarshalIndent(r, "", "  ")
		require.NoError(t, e)
		require.NoError(t, os.MkdirAll(filepath.Dir(output), 0755))
		require.NoError(t, os.WriteFile(output, append(data, '\n'), 0644))
	}()
	for _, count := range []int{1, 3} {
		if !t.Run(fmt.Sprintf("%d_node_cluster", count), func(t *testing.T) {
			opts := []suite.Option{suite.WithManagerHTTP()}
			for i := 1; i <= count; i++ {
				opts = append(opts, suite.WithNodeConfigOverrides(uint64(i), map[string]string{"WK_BENCH_API_ENABLE": "true", "WK_BENCH_API_TOKEN": "conversation-qps-fixture", "WK_BENCH_API_MAX_BATCH_SIZE": "600", "WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_CLUSTER_CHANNEL_REPLICA_N": fmt.Sprint(count)}), suite.WithNodeEnv(uint64(i), "GOMAXPROCS=2"))
			}
			cluster := suite.New(t).StartStaticCluster(count, opts...)
			binary, err := os.ReadFile(cluster.Nodes[0].Process.BinaryPath)
			require.NoError(t, err)
			digest := sha256.Sum256(binary)
			r.BinarySHA256 = hex.EncodeToString(digest[:])
			ready(t, cluster)
			setupStart := time.Now()
			expected := prepare(t, cluster, p)
			t.Logf("Prepared %d persisted channels in %s; safely evicting benchmark runtimes", len(expected), time.Since(setupStart))
			client := &http.Client{Transport: &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 16, MaxConnsPerHost: 16}, Timeout: 5 * time.Second}
			defer client.CloseIdleConnections()
			initial := evictFixtureRuntimes(t, cluster, client, p)
			for _, c := range p.Cases {
				// Untimed warmup is explicit; it must also leave the runtime-load counter unchanged.
				for i := 0; i < p.Cohorts*p.UsersPerCohort; i++ {
					require.NoError(t, request(context.Background(), client, cluster.Nodes[i%count].APIAddr(), p, c, i, expected))
				}
				before := snapshot(t, cluster)
				require.Equal(t, initial.loads, before.loads, "warmup must not load runtimes")
				require.Zero(t, before.active)
				require.Equal(t, initial.writes, before.writes, "warmup must not mutate memberships")
				result := measure(cluster, client, p, c, expected)
				after := snapshot(t, cluster)
				result.RuntimeLoads = after.loads - before.loads
				result.ActiveRuntimesBefore = before.active
				result.ActiveRuntimesAfter = after.active
				result.MembershipWrites = after.writes - before.writes
				if before.cpuAvailable && after.cpuAvailable {
					cpuSeconds := after.cpu - before.cpu
					result.CPUSeconds = &cpuSeconds
				}
				result.AllocatedBytes = after.allocated - before.allocated
				result.HeapBytes = after.heap
				e := evaluate(p, result)
				result.Verdict = "pass"
				if e != nil {
					result.Verdict = e.Error()
				}
				r.Phases = append(r.Phases, result)
				data, _ := json.Marshal(result)
				t.Logf("CONVERSATION_QPS %s", data)
				require.NoError(t, e, cluster.DumpDiagnostics())
			}
			if withCapacity {
				for _, w := range p.Cases {
					if w.PageSize == 100 {
						r.Capacity = append(r.Capacity, measureCapacityCase(t, cluster, client, p, w, expected, initial, ""))
					}
				}
			}

			if count == 3 {
				runReleaseStress(t, cluster, client, p, p.StressGate, expected, initial, func(w stressWindow) { r.Stress = append(r.Stress, w) })
			}

		}) {
			break
		}
	}
}

func ready(t *testing.T, c *suite.StartedCluster) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	require.NoError(t, c.WaitClusterReady(ctx), c.DumpDiagnostics())
	_, err := c.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err, c.DumpDiagnostics())
}

// prepare creates the fixed persisted dataset through public cluster entrypoints.
func prepare(t *testing.T, c *suite.StartedCluster, p profile) expectedMessages {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	expected := expectedMessages{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan int)
	errs := make(chan error, p.Cohorts*p.ChannelsPerCohort)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				cohort := n / p.ChannelsPerCohort
				id := fmt.Sprintf("qps-%d-%d", cohort, n%p.ChannelsPerCohort)
				sender := "qps-sender"
				users := []string{sender}
				for u := 0; u < p.UsersPerCohort; u++ {
					users = append(users, fmt.Sprintf("qps-user-%d", cohort*p.UsersPerCohort+u))
				}
				addr := c.Nodes[n%len(c.Nodes)].APIAddr()
				if e := suite.PostChannel(ctx, addr, map[string]any{"channel_id": id, "channel_type": 2, "subscribers": users}); e != nil {
					errs <- e
					continue
				}
				var messages []messageIdentity
				for seq := 0; seq < p.MessagesPerChannel; seq++ {
					no := fmt.Sprintf("%s-%d", id, seq)
					payload := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", p.PayloadBytes)))
					got, e := suite.PostMessageSendEventually(ctx, addr, map[string]any{"from_uid": sender, "channel_id": id, "channel_type": 2, "client_msg_no": no, "payload": payload})
					if e != nil {
						errs <- e
						break
					}
					if got.Reason != 1 || got.MessageSeq == 0 {
						errs <- fmt.Errorf("fixture SEND rejected")
						break
					}
					messages = append(messages, messageIdentity{MessageSeq: uint64(got.MessageSeq), ClientMsgNo: no, Payload: payload})
				}
				mu.Lock()
				expected[id] = messages
				mu.Unlock()
			}
		}()
	}
	for n := 0; n < p.Cohorts*p.ChannelsPerCohort; n++ {
		jobs <- n
	}
	close(jobs)
	wg.Wait()
	close(errs)
	t.Logf("Fixture produced %d/%d channel histories", len(expected), p.Cohorts*p.ChannelsPerCohort)
	for e := range errs {
		require.NoError(t, e, c.DumpDiagnostics())
	}
	require.Len(t, expected, p.Cohorts*p.ChannelsPerCohort)
	return expected
}

// request validates the full response and message identities before counting success.
func request(ctx context.Context, client *http.Client, addr string, p profile, c workloadCase, user int, expected expectedMessages) error {
	uid := fmt.Sprintf("qps-user-%d", user)
	body := map[string]any{"uid": uid, "limit": c.PageSize}
	if c.Endpoint == "/conversation/sync" {
		body = map[string]any{"uid": uid, "version": 0, "msg_count": p.MessagesPerChannel, "page": 1, "page_size": c.PageSize}
	}
	data, err := conversationResponse(ctx, client, addr, c.Endpoint, body)
	if err != nil {
		return err
	}

	type row struct {
		ChannelType int               `json:"channel_type"`
		ChannelID   string            `json:"channel_id"`
		Last        *messageIdentity  `json:"last_message"`
		Recents     []messageIdentity `json:"recents"`
	}
	var rows []row
	if c.Endpoint == "/conversation/list" {
		var page struct {
			Rows    []row             `json:"conversations"`
			Done    bool              `json:"done"`
			Deletes []json.RawMessage `json:"deletes"`
			Cursor  string            `json:"next_cursor"`
		}
		if err = json.Unmarshal(data, &page); err != nil {
			return err
		}
		if page.Done != (c.PageSize == p.ChannelsPerCohort) || len(page.Deletes) != 0 || page.Cursor == "" {
			return fmt.Errorf("invalid list coverage")
		}
		rows = page.Rows
	} else {
		if err = json.Unmarshal(data, &rows); err != nil {
			return err
		}
	}
	expectedIDs := cohortChannels(p, user/p.UsersPerCohort)
	if c.Endpoint == "/conversation/list" {
		sort.Slice(expectedIDs, func(i, j int) bool {
			if len(expectedIDs[i]) != len(expectedIDs[j]) {
				return len(expectedIDs[i]) < len(expectedIDs[j])
			}
			return expectedIDs[i] < expectedIDs[j]
		})
	}
	for i, row := range rows {
		if i >= c.PageSize || row.ChannelType != 2 || row.ChannelID != expectedIDs[i] {
			return fmt.Errorf("incorrect conversation page/order at row %d", i)
		}
	}
	if len(rows) != c.PageSize {
		return fmt.Errorf("partial response: %d rows", len(rows))
	}
	seen := map[string]bool{}
	prefix := fmt.Sprintf("qps-%d-", user/p.UsersPerCohort)
	for _, r := range rows {
		if !strings.HasPrefix(r.ChannelID, prefix) || seen[r.ChannelID] {
			return fmt.Errorf("wrong or duplicate channel")
		}
		seen[r.ChannelID] = true
		want, ok := expected[r.ChannelID]
		if !ok || len(want) != p.MessagesPerChannel {
			return fmt.Errorf("fixture identity missing")
		}
		if c.Endpoint == "/conversation/list" {
			if r.Last == nil || *r.Last != want[len(want)-1] {
				return fmt.Errorf("incorrect persisted head")
			}
		} else {
			if len(r.Recents) != len(want) {
				return fmt.Errorf("missing recents")
			}
			for i, m := range r.Recents {
				if m != want[len(want)-1-i] {
					return fmt.Errorf("incorrect recent message")
				}
			}
		}
	}
	return nil
}

type observation struct {
	driverWait, requestTime float64
	latency                 float64
	within                  bool
	err                     error
}

// measure schedules arrivals independently of completions. Queueing counts toward
// latency, while late drain completions cannot inflate fixed-window throughput.
func measure(c *suite.StartedCluster, client *http.Client, p profile, w workloadCase, expected expectedMessages) phaseResult {
	return measureRequests(p, w, len(c.Nodes), time.Now(), func(ctx context.Context, index int) error {
		return request(ctx, client, c.Nodes[index%len(c.Nodes)].APIAddr(), p, w, index%(p.Cohorts*p.UsersPerCohort), expected)
	})
}

// measureRequests shares one scheduled start across concurrent endpoint streams.
func measureRequests(p profile, w workloadCase, nodes int, start time.Time, call func(context.Context, int) error) phaseResult {
	total := w.OfferedQPS * p.DurationSeconds
	results := make(chan observation, total)
	// Retain at most one latency budget of arrivals. Scheduling and queue delay
	// still count toward P99, and sustained overload eventually drops and fails.
	jobs := make(chan struct {
		at    time.Time
		index int
	}, queuedArrivalLimit(p, w))
	var wg sync.WaitGroup
	end := start.Add(time.Duration(p.DurationSeconds) * time.Second)
	for i := 0; i < p.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				ctx, cancel := context.WithDeadline(context.Background(), j.at.Add(5*time.Second))
				requestStart := time.Now()
				e := call(ctx, j.index)
				cancel()
				now := time.Now()
				results <- observation{latency: float64(now.Sub(j.at)) / float64(time.Millisecond), within: !now.After(end), err: e, driverWait: float64(requestStart.Sub(j.at)) / float64(time.Millisecond), requestTime: float64(now.Sub(requestStart)) / float64(time.Millisecond)}
			}
		}()
	}
	r := phaseResult{DriverWorkers: p.Workers, QueueCapacity: queuedArrivalLimit(p, w), Nodes: nodes, Case: w, Scheduled: total, DurationSeconds: p.DurationSeconds}
	for i := 0; i < total; i++ {
		at := start.Add(time.Duration(i) * time.Second / time.Duration(w.OfferedQPS))
		if delay := time.Until(at); delay > 0 {
			time.Sleep(delay)
		}
		select {
		case jobs <- struct {
			at    time.Time
			index int
		}{at, i}:
		default:
			r.Dropped++
		}
	}
	close(jobs)
	wg.Wait()
	close(results)
	var latencies, driverWaits, requestTimes []float64
	for got := range results {
		if got.err != nil {
			r.Errors++
			if r.ErrorSamples == nil {
				r.ErrorSamples = make(map[string]int)
			}
			label := got.err.Error()
			if len(label) > 512 {
				label = label[:512]
			}
			if _, ok := r.ErrorSamples[label]; ok || len(r.ErrorSamples) < 8 {
				r.ErrorSamples[label]++
			}
			var status *responseStatusError
			if !errors.As(got.err, &status) || !status.capacityRefusal() {
				r.UnexpectedErrors++
				if r.FirstUnexpectedError == "" {
					r.FirstUnexpectedError = got.err.Error()
				}
			}
			if r.FirstError == "" {
				r.FirstError = got.err.Error()
			}
		} else {
			r.Completed++
			if got.within {
				r.CompletedInWindow++
			}
		}
		latencies = append(latencies, got.latency)
		driverWaits = append(driverWaits, got.driverWait)
		requestTimes = append(requestTimes, got.requestTime)
	}
	r.ActualQPS = float64(r.CompletedInWindow) / float64(p.DurationSeconds)
	r.P50MS = percentile(latencies, .50)
	r.P95MS = percentile(latencies, .95)
	r.P99MS = percentile(latencies, .99)
	r.DriverWaitP99MS = percentile(driverWaits, .99)
	r.RequestP99MS = percentile(requestTimes, .99)
	return r
}

type metricsSnapshot struct {
	loads, writes, cpu, allocated, heap, active float64
	cpuAvailable                                bool
}

// snapshot requires observed counters on every node; only non-Linux process CPU
// may be absent, and that absence is encoded as null in the diagnostic report.
func snapshot(t *testing.T, c *suite.StartedCluster) metricsSnapshot {
	t.Helper()
	r := metricsSnapshot{cpuAvailable: true}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	names := []string{"wukongim_channelv2_runtime_load_total", "wukongim_conversation_membership_mutation_rows_total", "process_cpu_seconds_total", "go_memstats_alloc_bytes_total", "go_memstats_heap_alloc_bytes", "wukongim_channelv2_active_runtimes"}
	for _, node := range c.Nodes {
		samples, err := suite.FetchMetricSamples(ctx, node.APIAddr())
		require.NoError(t, err)
		values := make([]float64, len(names))
		for i, name := range names {
			found := false
			for _, s := range samples {
				if s.Name == name {
					found = true
					values[i] += s.Value
				}
			}
			if !found && name == "process_cpu_seconds_total" && runtime.GOOS != "linux" {
				r.cpuAvailable = false
				continue
			}
			require.True(t, found, "required metric missing: %s", name)
		}
		r.loads += values[0]
		r.writes += values[1]
		r.cpu += values[2]
		r.allocated += values[3]
		r.heap += values[4]
		r.active += values[5]
	}
	return r
}

// evictFixtureRuntimes uses the existing authenticated benchmark control surface
// to release only this fixture's generated channels. Busy runtimes retain their
// safety guards. HTTP/metric errors fail immediately; only reported busy work
// may settle within the bounded preparation deadline. No eviction runs during reads.
func evictFixtureRuntimes(t *testing.T, c *suite.StartedCluster, client *http.Client, p profile) metricsSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	evicted := 0
	for {
		busy := 0
		for _, node := range c.Nodes {
			for cohort := 0; cohort < p.Cohorts; cohort++ {
				body, _ := json.Marshal(map[string]any{"run_id": "qps", "profile": fmt.Sprint(cohort), "channel_type": 2, "range": map[string]int{"start": 0, "end": p.ChannelsPerCohort}})
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+node.APIAddr()+"/bench/v1/channel-runtime/evict", bytes.NewReader(body))
				require.NoError(t, err)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer conversation-qps-fixture")
				resp, err := client.Do(req)
				require.NoError(t, err)
				raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
				resp.Body.Close()
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
				var result struct {
					Version   string `json:"version"`
					NodeID    uint64 `json:"node_id"`
					Requested int    `json:"requested"`
					Evicted   int    `json:"evicted"`
					Missing   int    `json:"missing"`
					Busy      int    `json:"skipped_busy"`
				}
				require.NoError(t, json.Unmarshal(raw, &result))
				require.Equal(t, "bench/v1", result.Version)
				require.Equal(t, node.Spec.ID, result.NodeID)
				require.Equal(t, p.ChannelsPerCohort, result.Requested)
				require.Equal(t, result.Requested, result.Evicted+result.Missing+result.Busy)
				busy += result.Busy
				evicted += result.Evicted
			}
		}
		current := snapshot(t, c)
		if busy == 0 && current.active == 0 {
			t.Logf("Cold runtimes verified on every node: evicted=%d, historical fixture loads=%g", evicted, current.loads)
			return current
		}
		select {
		case <-ctx.Done():
			t.Fatalf("benchmark runtime eviction did not complete: busy=%d active=%g", busy, current.active)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Exercise the real request error path, including its bounded response body.
func TestConversationRequestPreservesRefusalEnvelope(t *testing.T) {
	p, _, err := loadProfile()
	require.NoError(t, err)
	client := &http.Client{Transport: refusalRoundTripper{}}
	err = request(context.Background(), client, "127.0.0.1:1", p, p.Cases[4], 0, nil)
	var status *responseStatusError
	require.ErrorAs(t, err, &status)
	require.True(t, status.capacityRefusal(), "%s", err)
}

type refusalRoundTripper struct{}

func (refusalRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"msg":"internal/message: backpressured: channel: backpressured","status":400}`)), Header: make(http.Header)}, nil
}

// conversationResponse preserves typed HTTP refusals for all load scenarios.
func conversationResponse(ctx context.Context, client *http.Client, addr, endpoint string, body map[string]any) ([]byte, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+strings.TrimPrefix(addr, "http://")+endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 2<<20 {
		return nil, fmt.Errorf("oversized response")
	}
	if resp.StatusCode != 200 {
		return nil, &responseStatusError{code: resp.StatusCode, body: string(data[:min(len(data), 256)])}
	}
	return data, nil
}
