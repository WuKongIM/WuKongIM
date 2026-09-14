//go:build e2e

package conversation_qps

import (
	"bytes"
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
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

// TestMessageUpdateThreeNodeSoak is a same-host Linux regression diagnostic.
// It preserves the release dataset and mixed rates, but cannot qualify a release
// or establish capacity across physical hosts. Editing is an explicit variant.
func TestMessageUpdateThreeNodeSoak(t *testing.T) {
	if os.Getenv("WK_E2E_MESSAGE_UPDATE_SOAK") != "1" {
		t.Skip("explicit three-node message update diagnostic")
	}
	require.Equal(t, "linux", runtime.GOOS)
	output := os.Getenv("WK_E2E_MESSAGE_UPDATE_REPORT")
	require.NotEmpty(t, output)
	require.NoError(t, os.MkdirAll(filepath.Dir(output), 0755))
	edited := os.Getenv("WK_E2E_MESSAGE_UPDATE_EDITED") == "1"
	deltaProfile := os.Getenv("WK_E2E_MESSAGE_UPDATE_DELTA_PROFILE") == "1"
	if deltaProfile {
		require.True(t, edited, "delta profiling requires edited fixture")
	}
	p, raw, err := loadProfile()
	require.NoError(t, err)
	profileHash := sha256.Sum256(raw)
	harness, err := exec.Command("git", "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	r := struct {
		Schema           string         `json:"schema"`
		HarnessBase      string         `json:"harness_base"`
		BinarySHA        string         `json:"binary_sha256"`
		ProfileSHA       string         `json:"profile_sha256"`
		OS               string         `json:"os"`
		Arch             string         `json:"arch"`
		CPUs             int            `json:"cpus"`
		NodeGOMAXPROCS   int            `json:"node_gomaxprocs"`
		DriverGOMAXPROCS int            `json:"driver_gomaxprocs"`
		Edited           bool           `json:"edited"`
		DeltaProfile     bool           `json:"delta_profile,omitempty"`
		DeltaWarmup      *soakWarmup    `json:"delta_warmup,omitempty"`
		Windows          []stressWindow `json:"windows"`
		Profiles         []string       `json:"profiles"`
		Complete         bool           `json:"complete"`
	}{Schema: "wukongim/message-update-soak/v1", HarnessBase: string(bytes.TrimSpace(harness)), ProfileSHA: hex.EncodeToString(profileHash[:]), OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), NodeGOMAXPROCS: 2, DriverGOMAXPROCS: runtime.GOMAXPROCS(0), Edited: edited}
	r.DeltaProfile = deltaProfile
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
	binary, e := os.ReadFile(c.Nodes[0].Process.BinaryPath)
	require.NoError(t, e)
	digest := sha256.Sum256(binary)
	r.BinarySHA = hex.EncodeToString(digest[:])
	save()
	ready(t, c)
	expected := prepare(t, c, p)
	client := &http.Client{Transport: &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 16, MaxConnsPerHost: 16}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	cursor := ""
	if edited {
		cursor = editSoakFixture(t, c, client, expected)
	}
	initial := evictFixtureRuntimes(t, c, client, p)
	for i := 0; i < 3; i++ {
		w := measureMixed(t, c, client, p, p.StressGate, expected, initial)
		r.Windows = append(r.Windows, w)
		save()
		checkStressWindow(t, w, false)
	}
	if edited {
		// Fixed cursors deliberately replay one changed page versus an empty page.
		// Every response is validated; these are read costs, not SDK polling advice.
		cold := snapshot(t, c)
		warmStart := time.Now()
		changed, e := soakDelta(context.Background(), client, c.Nodes[0].APIAddr(), cursor)
		warmDuration := time.Since(warmStart)
		require.NoError(t, e)
		require.Len(t, changed.Updates, 1)
		warm := snapshot(t, c)
		r.DeltaWarmup = &soakWarmup{DurationMS: float64(warmDuration) / float64(time.Millisecond), RuntimeLoads: warm.loads - cold.loads, ActiveBefore: cold.active, ActiveAfter: warm.active, MembershipWrites: warm.writes - cold.writes}
		save()
		require.LessOrEqual(t, warm.loads-cold.loads, float64(3), "only the selected channel may recover")
		require.LessOrEqual(t, warm.active, float64(3))
		require.Equal(t, cold.writes, warm.writes)
		for _, mode := range []struct {
			name, cursor string
			count        int
		}{{"delta_changed", cursor, 1}, {"delta_empty", changed.Cursor, 0}} {
			pp := p
			pp.DurationSeconds = 60
			pp.Workers = 8
			work := workloadCase{Endpoint: "/channel/messageupdates", PageSize: 100, OfferedQPS: 100}
			before := snapshot(t, c)
			w := stressWindow{Name: mode.name, Before: diagnosticMetrics(t, c)}
			start := time.Now()
			w.Start = start.UTC()
			w.End = w.Start.Add(time.Minute)
			phase := measureRequests(pp, work, 3, start, func(ctx context.Context, index int) error {
				got, e := soakDelta(ctx, client, c.Nodes[index%3].APIAddr(), mode.cursor)
				if e != nil {
					return e
				}
				if got.Reset || got.More || got.Cursor == "" || len(got.Updates) != mode.count {
					return fmt.Errorf("invalid delta coverage")
				}
				if mode.count == 1 && (got.Updates[0].Version != "1" || got.Updates[0].Payload != expected["qps-0-0"][2].Payload || got.Updates[0].Seq != "3") {
					return fmt.Errorf("incorrect edited delta")
				}
				return nil
			})
			w.Phases = []phaseResult{phase}
			finishSoakDeltaWindow(t, c, &w, before, pp)
			r.Windows = append(r.Windows, w)
			save()
			require.Zero(t, w.RuntimeLoads)
			require.Zero(t, w.MembershipWrites)
			require.Equal(t, w.ActiveBefore, w.ActiveAfter)
			require.Zero(t, w.Phases[0].UnexpectedErrors, w.Phases[0].FirstUnexpectedError)
		}
		if !deltaProfile {
			initial = evictFixtureRuntimes(t, c, client, p)
		}
	}
	// Profiles run only after all unprofiled windows, with an independent ten-second load.
	if deltaProfile {
		r.Profiles = captureSoakProfiles(t, c, client, filepath.Dir(output), func() {
			pp := p
			pp.DurationSeconds = 10
			pp.Workers = 8
			before := snapshot(t, c)
			w := stressWindow{Name: "profile_delta_changed", Before: diagnosticMetrics(t, c)}
			start := time.Now()
			w.Start, w.End = start.UTC(), start.UTC().Add(10*time.Second)
			phase := measureRequests(pp, workloadCase{Endpoint: "/channel/messageupdates", PageSize: 100, OfferedQPS: 100}, 3, start, func(ctx context.Context, index int) error {
				got, err := soakDelta(ctx, client, c.Nodes[index%3].APIAddr(), cursor)
				if err != nil {
					return err
				}
				if got.Reset || got.More || got.Cursor == "" || len(got.Updates) != 1 || got.Updates[0].Version != "1" || got.Updates[0].Seq != "3" || got.Updates[0].Payload != expected["qps-0-0"][2].Payload {
					return fmt.Errorf("incorrect profiled delta")
				}
				return nil
			})
			w.Phases = []phaseResult{phase}
			finishSoakDeltaWindow(t, c, &w, before, pp)
			require.Zero(t, phase.Errors)
			require.Zero(t, phase.Dropped)
			require.Zero(t, w.RuntimeLoads)
			require.Zero(t, w.MembershipWrites)
			require.Equal(t, w.ActiveBefore, w.ActiveAfter)
		})
	} else {
		r.Profiles = profileSoak(t, c, client, p, expected, initial, filepath.Dir(output))
	}
	r.Complete = true
}

// soakWarmup records the one selected channel's committed-read recovery separately
// from steady delta load. Conversation-only windows still require zero residency.
type soakWarmup struct {
	DurationMS       float64 `json:"duration_ms"`
	RuntimeLoads     float64 `json:"runtime_loads"`
	ActiveBefore     float64 `json:"active_before"`
	ActiveAfter      float64 `json:"active_after"`
	MembershipWrites float64 `json:"membership_writes"`
}

func finishSoakDeltaWindow(t *testing.T, c *suite.StartedCluster, w *stressWindow, before metricsSnapshot, p profile) {
	t.Helper()
	after := snapshot(t, c)
	w.After = diagnosticMetrics(t, c)
	require.True(t, before.cpuAvailable && after.cpuAvailable)
	w.CPUSeconds = after.cpu - before.cpu
	w.AllocatedBytes = after.allocated - before.allocated
	w.HeapBytes = after.heap
	w.RuntimeLoads = after.loads - before.loads
	w.MembershipWrites = after.writes - before.writes
	w.ActiveBefore = before.active
	w.ActiveAfter = after.active
	phase := &w.Phases[0]
	phase.RuntimeLoads = w.RuntimeLoads
	phase.MembershipWrites = w.MembershipWrites
	// Evaluate the unchanged rate/latency/error budgets before attaching observed
	// residency: this selected channel was explicitly recovered outside the window.
	phase.Verdict = "pass"
	if e := evaluate(p, *phase); e != nil {
		phase.Verdict = e.Error()
	}
	phase.ActiveRuntimesBefore = w.ActiveBefore
	phase.ActiveRuntimesAfter = w.ActiveAfter
	if w.ActiveBefore != w.ActiveAfter {
		phase.Verdict = "delta changed runtime residency"
	}
	t.Logf("DELTA name=%s actual=%.2f p99=%.2f errors=%d drops=%d active=%.0f/%.0f loads=%.0f verdict=%s", w.Name, phase.ActualQPS, phase.P99MS, phase.Errors, phase.Dropped, w.ActiveBefore, w.ActiveAfter, w.RuntimeLoads, phase.Verdict)
}

type soakDeltaPage struct {
	Cursor  string `json:"next_update_cursor"`
	More    bool   `json:"more"`
	Reset   bool   `json:"reset_required"`
	Updates []struct {
		Version string `json:"version"`
		Payload string `json:"payload"`
		Seq     string `json:"message_seq"`
	} `json:"updates"`
}

func soakDelta(ctx context.Context, client *http.Client, addr, cursor string) (soakDeltaPage, error) {
	raw, e := conversationResponse(ctx, client, addr, "/channel/messageupdates", map[string]any{"login_uid": "qps-user-0", "channel_id": "qps-0-0", "channel_type": 2, "update_cursor": cursor, "limit": 100})
	var page soakDeltaPage
	if e == nil {
		e = json.Unmarshal(raw, &page)
	}
	return page, e
}

// editSoakFixture changes every retained tail once, retaining the same payload
// size and conversation ordering. All mutations finish before measured reads.
func editSoakFixture(t *testing.T, c *suite.StartedCluster, client *http.Client, expected expectedMessages) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	baseline, e := soakDelta(ctx, client, c.Nodes[0].APIAddr(), "")
	require.NoError(t, e)
	require.True(t, baseline.Reset)
	require.NotEmpty(t, baseline.Cursor)
	ids := make([]string, 0, len(expected))
	for id := range expected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for i, id := range ids {
		body, _ := json.Marshal(map[string]any{"login_uid": "qps-sender", "channel_id": id, "channel_type": 2, "limit": 3})
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+c.Nodes[i%3].APIAddr()+"/channel/messagesync", bytes.NewReader(body))
		require.NoError(t, e)
		req.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(req)
		require.NoError(t, e)
		raw, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		require.NoError(t, e)
		require.Equal(t, 200, resp.StatusCode)
		epoch := resp.Header.Get("X-WK-Content-Epoch")
		require.NotEmpty(t, epoch)
		var history struct {
			Messages []struct {
				ID  json.Number `json:"message_id"`
				Seq uint64      `json:"message_seq"`
			} `json:"messages"`
		}
		require.NoError(t, json.Unmarshal(raw, &history))
		target := ""
		for _, m := range history.Messages {
			if m.Seq == 3 {
				target = m.ID.String()
			}
		}
		require.NotEmpty(t, target)
		payload := bytes.Repeat([]byte("e"), 256)
		raw, e = conversationResponse(ctx, client, c.Nodes[(i+1)%3].APIAddr(), "/message/update", map[string]any{"login_uid": "qps-sender", "channel_id": id, "channel_type": 2, "message_id": target, "expected_content_epoch": epoch, "expected_version": "0", "request_id": "soak-edit-" + strconv.Itoa(i), "payload": payload})
		require.NoError(t, e)
		var update struct {
			Data struct {
				Version string `json:"version"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(raw, &update))
		require.Equal(t, "1", update.Data.Version)
		// Marshal the bytes through JSON to preserve the legacy base64 payload shape.
		encoded, _ := json.Marshal(payload)
		require.NoError(t, json.Unmarshal(encoded, &expected[id][2].Payload))
	}
	t.Logf("Edited and validated %d retained tails before measured reads", len(ids))
	return baseline.Cursor
}

func profileSoak(t *testing.T, c *suite.StartedCluster, client *http.Client, p profile, expected expectedMessages, initial metricsSnapshot, dir string) []string {
	t.Helper()
	return captureSoakProfiles(t, c, client, dir, func() {
		cfg := p.StressGate
		cfg.MixedSeconds = 10
		w := measureMixed(t, c, client, p, cfg, expected, initial)
		checkStressWindow(t, w, false)
	})
}

// captureSoakProfiles bounds per-node sampling around an independent workload.
// Profiled traffic never contributes to the unprofiled acceptance windows.
func captureSoakProfiles(t *testing.T, c *suite.StartedCluster, client *http.Client, dir string, workload func()) []string {
	t.Helper()
	profileClient := &http.Client{Transport: client.Transport, Timeout: 15 * time.Second}
	var paths []string
	fetch := func(addr, route, path string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/debug/pprof/"+route, nil)
		if e != nil {
			return e
		}
		req.Header.Set("Authorization", "Bearer conversation-qps-fixture")
		resp, e := profileClient.Do(req)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("profile HTTP %d", resp.StatusCode)
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, (32<<20)+1))
		if e != nil {
			return e
		}
		if len(b) > 32<<20 {
			return fmt.Errorf("profile exceeds bound")
		}
		return os.WriteFile(path, b, 0644)
	}
	for i, n := range c.Nodes {
		path := filepath.Join(dir, fmt.Sprintf("node%d-alloc-before.pprof", i+1))
		require.NoError(t, fetch(n.APIAddr(), "allocs", path))
		paths = append(paths, path)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i, n := range c.Nodes {
		path := filepath.Join(dir, fmt.Sprintf("node%d-cpu.pprof", i+1))
		paths = append(paths, path)
		wg.Add(1)
		go func() { defer wg.Done(); errs <- fetch(n.APIAddr(), "profile?seconds=8", path) }()
	}
	workload()
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	for i, n := range c.Nodes {
		path := filepath.Join(dir, fmt.Sprintf("node%d-alloc-after.pprof", i+1))
		require.NoError(t, fetch(n.APIAddr(), "allocs", path))
		paths = append(paths, path)
	}
	return paths
}
