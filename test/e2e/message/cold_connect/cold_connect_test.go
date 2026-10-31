//go:build e2e

package cold_connect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

type connectOutcome struct {
	Category       string `json:"category"`
	Phase          string `json:"phase,omitempty"`
	Kind           string `json:"kind,omitempty"`
	ReasonKnown    bool   `json:"reason_known"`
	Reason         uint8  `json:"reason"`
	ArrivalLagNS   int64  `json:"arrival_lag_ns"`
	ElapsedNS      int64  `json:"elapsed_ns"`
	TokenElapsedNS int64  `json:"token_elapsed_ns"`
}

// TestColdConnect keeps the first result of every fixed-arrival authenticated login.
func TestColdConnect(t *testing.T) {
	if os.Getenv("WK_E2E_COLD_CONNECT") != "1" {
		t.Skip("opt-in cold CONNECT reduction")
	}
	dir := os.Getenv("WK_E2E_COLD_CONNECT_REPORT_DIR")
	require.NotEmpty(t, dir, "one explicit artifact directory required")
	arrival := os.Getenv("WK_E2E_COLD_CONNECT_ARRIVAL")
	if arrival == "" {
		arrival = "paced"
	}
	require.Contains(t, []string{"paced", "one_second_burst"}, arrival, "fixed diagnostic arrival shape required")
	tokenTiming := os.Getenv("WK_E2E_COLD_CONNECT_TOKEN_TIMING")
	if tokenTiming == "" {
		tokenTiming = "preprovisioned"
	}
	require.Contains(t, []string{"preprovisioned", "on_arrival"}, tokenTiming, "fixed diagnostic token timing required")
	require.NoError(t, os.MkdirAll(dir, 0700))
	users := 300
	if population := os.Getenv("WK_E2E_COLD_CONNECT_POPULATION"); population != "" {
		require.Equal(t, "10000", population, "only the fixed10k population control is supported")
		users = 10000
	}
	var cluster *suite.StartedCluster
	var processes []*suite.NodeProcess
	clients := make([]*suite.WKProtoClient, users)
	outcomes := make([]connectOutcome, users)
	phase := "startup"
	clientsClosed, cohortJoined := false, false
	var binarySHA string
	// Register before the suite so the receipt observes its joined process cleanup.
	t.Cleanup(func() {
		exited, joined := len(processes) == 3, len(processes) == 3
		for _, process := range processes {
			if process.Stop() != nil {
				joined = false
			}
			_, done := process.ExitResult()
			exited = exited && done
		}
		if !exited || !joined {
			t.Error("process cleanup incomplete")
		}
		report := map[string]any{"schema": "cold-connect-native/v1", "phase": phase,
			"scope": "cold CONNECT diagnostic without business SEND or full-sync, not2h acceptance",
			"nodes": 3, "hash_slots": 256, "logical_slots": 12, "replicas": 3,
			"planned_users": users, "first_connect_per_second": 100, "server_activation_seconds": 3,
			"arrival_shape": arrival,
			"token_timing":  tokenTiming, "token_api": "public_user_token",
			"client_seconds": 5, "retry_count": 0, "top_history": false, "binary_sha256": binarySHA,
			"outcomes": outcomes, "cohort_joined": cohortJoined, "clients_closed": clientsClosed,
			"processes_exited": exited, "process_groups_joined": joined, "passed": !t.Failed() && cohortJoined && clientsClosed && exited && joined}
		writeJSON(t, filepath.Join(dir, "final.json"), report, 4<<20)
	})
	opts := []suite.Option{suite.WithManagerHTTP(), suite.WithWorkspaceRootDir(filepath.Join(dir, "workspace"))}
	for i := 1; i <= 3; i++ {
		opts = append(opts, suite.WithNodeConfigOverrides(uint64(i), map[string]string{
			"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12",
			"WK_CLUSTER_SLOT_REPLICA_N": "3", "WK_CLUSTER_CHANNEL_REPLICA_N": "3",
			"WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_TOP_API_ENABLE": "false",
			"WK_METRICS_ENABLE": "true", "WK_DEBUG_API_ENABLE": "true",
			"WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW": "500us",
			"WK_CLUSTER_COMMIT_COORDINATOR_SHARDS":       "1", "WK_CLUSTER_COMMIT_COORDINATOR_SYNC": "true",
		}), suite.WithNodeEnv(uint64(i), "GOMAXPROCS=2"))
	}
	cluster = suite.New(t).StartStaticCluster(3, opts...)
	for _, node := range cluster.Nodes {
		processes = append(processes, node.Process)
	}
	t.Cleanup(func() {
		clientsClosed = true
		for _, client := range clients {
			if client != nil && client.Close() != nil {
				clientsClosed = false
				t.Error("client cleanup incomplete")
			}
		}
	})
	f, err := os.Open(cluster.MustNode(1).Process.BinaryPath)
	require.NoError(t, err)
	h := sha256.New()
	n, copyErr := io.Copy(h, io.LimitReader(f, (128<<20)+1))
	closeErr := f.Close()
	require.NoError(t, copyErr)
	require.NoError(t, closeErr)
	require.LessOrEqual(t, n, int64(128<<20))
	binarySHA = hex.EncodeToString(h.Sum(nil))
	budget := 110 * time.Second
	if users == 10000 {
		budget = 180 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	ready, done := context.WithTimeout(ctx, 35*time.Second)
	require.NoError(t, cluster.WaitClusterReady(ready), "cluster readiness failed")
	_, err = cluster.WaitSlotLeadersStable(ready, time.Second)
	done()
	require.NoError(t, err, "Slot authority did not stabilize")
	phase = "token_setup"
	prepareToken := func(i int) error {
		uid := fmt.Sprintf("cold-native-%04d", i)
		call, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		_, err := suite.PostJSON(call, "http://"+cluster.MustNode(uint64(i%3+1)).APIAddr()+"/user/token", map[string]any{"uid": uid, "token": "cold-fixture-" + uid, "device_flag": int(frame.APP), "device_level": 0}, nil)
		return err
	}
	for i := 0; i < users; i++ {
		if tokenTiming == "preprovisioned" {
			require.True(t, prepareToken(i) == nil, "public token setup failed")
		}
		clients[i], err = suite.NewWKProtoClient()
		require.NoError(t, err)
	}
	captureMetrics(t, ctx, cluster, dir, "before")
	phase = "first_connect"
	start := time.Now()
	var probes sync.WaitGroup
	probeCtx, stopProbes := context.WithCancel(ctx)
	defer stopProbes()
	if os.Getenv("WK_E2E_COLD_CONNECT_STARTUP_STACKS") == "1" {
		probes.Add(1)
		go func() {
			defer probes.Done()
			for _, offset := range []time.Duration{2 * time.Second, 4 * time.Second} {
				timer := time.NewTimer(max(time.Until(start.Add(offset)), 0))
				select {
				case <-timer.C:
				case <-probeCtx.Done():
					timer.Stop()
					return
				}
				var nodes sync.WaitGroup
				for node := 1; node <= 3; node++ {
					nodes.Add(1)
					go func(node int) {
						defer nodes.Done()
						call, stop := context.WithTimeout(probeCtx, 2*time.Second)
						defer stop()
						began := time.Since(start)
						path := filepath.Join(dir, fmt.Sprintf("startup-%ds-stacks-node%d.txt", int(offset/time.Second), node))
						e := writeStackSummary(call, "http://"+cluster.MustNode(uint64(node)).APIAddr(), path)
						writeJSON(t, path+".receipt.json", map[string]any{"available": e == nil, "possibly_truncated": true,
							"began_elapsed_ns": began.Nanoseconds(), "ended_elapsed_ns": time.Since(start).Nanoseconds(), "scope": "diagnostic_only_not_unprofiled_qualification"}, 1024)
					}(node)
				}
				nodes.Wait()
			}
		}()
	}
	var wg sync.WaitGroup
	for i := 0; i < users; i++ {
		planned := start.Add(time.Duration(i) * 10 * time.Millisecond)
		if arrival == "one_second_burst" {
			planned = start.Add(time.Duration(i/100) * time.Second)
		}
		if wait := time.Until(planned); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
			}
		}
		wg.Add(1)
		go func(i int, planned time.Time) {
			defer wg.Done()
			began := time.Now()
			out := connectOutcome{Category: "transport_other", ArrivalLagNS: began.Sub(planned).Nanoseconds()}
			if tokenTiming == "on_arrival" {
				err := prepareToken(i)
				out.TokenElapsedNS = time.Since(began).Nanoseconds()
				if err != nil {
					out.Category = "token_preparation_failed"
					outcomes[i] = out
					return
				}
			}
			connectBegan := time.Now()
			call, stop := context.WithTimeout(ctx, 5*time.Second)
			defer stop()
			uid := fmt.Sprintf("cold-native-%04d", i)
			ack, e := clients[i].ConnectWithTokenContext(call, cluster.MustNode(uint64(i%3+1)).GatewayAddr(), uid, "cold-device", "cold-fixture-"+uid)
			out.ElapsedNS = time.Since(connectBegan).Nanoseconds()
			if e == nil && ack != nil {
				out.Category = "ok"
				out.ReasonKnown = true
				out.Reason = uint8(ack.ReasonCode)
			} else {
				var rejected interface {
					ConnackReason() (frame.ReasonCode, bool)
					ConnectPhase() string
					ConnectKind() string
				}
				if errors.As(e, &rejected) {
					r, known := rejected.ConnackReason()
					out.Phase, out.Kind = rejected.ConnectPhase(), rejected.ConnectKind()
					out.ReasonKnown = known
					out.Reason = uint8(r)
					if known {
						out.Category = "connack_rejected"
					} else {
						out.Category = out.Kind
					}
				} else if errors.Is(e, context.DeadlineExceeded) {
					out.Category = "client_deadline"
				} else if errors.Is(e, context.Canceled) {
					out.Category = "canceled"
				}
			}
			outcomes[i] = out
		}(i, planned)
	}
	wg.Wait()
	cohortJoined = true
	stopProbes()
	probes.Wait()
	captureMetrics(t, ctx, cluster, dir, "after")
	succeeded := 0
	for _, out := range outcomes {
		if out.Category == "ok" && out.Reason == uint8(frame.ReasonSuccess) {
			succeeded++
		}
	}
	if succeeded != users {
		phase = "connect_rejected"
		for i := 1; i <= 3; i++ {
			call, stop := context.WithTimeout(context.Background(), 2*time.Second)
			path := filepath.Join(dir, fmt.Sprintf("post-failure-stacks-node%d.txt", i))
			e := writeStackSummary(call, "http://"+cluster.MustNode(uint64(i)).APIAddr(), path)
			stop()
			observation := map[string]any{"available": e == nil, "possibly_truncated": true}
			if info, e := os.Stat(path); e == nil {
				observation["bytes"] = info.Size()
				observation["possibly_truncated"] = info.Size() >= 1<<20
			}
			writeJSON(t, path+".receipt.json", observation, 1024)
		}
		t.Errorf("first CONNECT succeeded %d/%d; closed outcomes retained", succeeded, users)
		return
	}
	phase = "complete"
}

func captureMetrics(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, dir, cut string) {
	t.Helper()
	for i := 1; i <= 3; i++ {
		call, stop := context.WithTimeout(ctx, 2*time.Second)
		samples, receipt, err := suite.FetchMetricSamplesWithReceipt(call, cluster.MustNode(uint64(i)).APIAddr())
		stop()
		selected := make([]suite.MetricSample, 0, 256)
		for _, sample := range samples {
			authPressure := strings.HasPrefix(sample.Name, "wukongim_runtime_") && sample.Labels["component"] == "gateway" && sample.Labels["pool"] == "async_auth"
			if authPressure || strings.HasPrefix(sample.Name, "wukongim_presence_") || strings.HasPrefix(sample.Name, "wukongim_gateway_auth_") || strings.HasPrefix(sample.Name, "wukongim_rpc_") || strings.HasPrefix(sample.Name, "wukongim_transport_") {
				selected = append(selected, sample)
			}
		}
		require.LessOrEqual(t, len(selected), 2048, "metric observation exceeded fixed bound")
		writeJSON(t, filepath.Join(dir, fmt.Sprintf("metrics-%s-node%d.json", cut, i)), map[string]any{"available": err == nil, "receipt": receipt, "selected_samples": selected}, 512<<10)
	}
}

// Aggregate stacks avoid spending the evidence cap on repeated client pumps.
// This post-cohort observation never changes the original CONNECT outcomes.
func writeStackSummary(ctx context.Context, base, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/debug/pprof/goroutine?debug=1", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("stack_summary_status")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		data = data[:1<<20]
	}
	return os.WriteFile(path, data, 0600)
}

func writeJSON(t *testing.T, path string, value any, cap int) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	require.LessOrEqual(t, len(data), cap)
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
}
