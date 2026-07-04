package transport

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	transportStressEnv            = "WK_TRANSPORT_STRESS"
	transportStressDurationEnv    = "WK_TRANSPORT_STRESS_DURATION"
	transportStressRaftWorkersEnv = "WK_TRANSPORT_STRESS_RAFT_WORKERS"
	transportStressRPCWorkersEnv  = "WK_TRANSPORT_STRESS_RPC_WORKERS"
	transportStressRPCDelayEnv    = "WK_TRANSPORT_STRESS_RPC_DELAY"
	transportStressSeedEnv        = "WK_TRANSPORT_STRESS_SEED"
	transportStressP99BudgetEnv   = "WK_TRANSPORT_STRESS_P99_BUDGET"
	transportStressNodeID         = NodeID(2)
	transportStressMsgType        = uint8(11)
)

type transportStressConfig struct {
	Enabled     bool
	Duration    time.Duration
	RaftWorkers int
	RPCWorkers  int
	RPCDelay    time.Duration
	Seed        int64
	P99Budget   time.Duration
}

type transportLatencySummary struct {
	Count int
	P50   time.Duration
	P95   time.Duration
	P99   time.Duration
	Max   time.Duration
}

type transportSampleCollector struct {
	mu        sync.Mutex
	latencies []time.Duration
	count     atomic.Uint64
}

type transportStressHarness struct {
	server     *Server
	raftClient *Client
	rpcClient  *Client
	raftStats  *transportSampleCollector
	rpcStats   *transportSampleCollector
	raftSent   atomic.Uint64
	raftErrors atomic.Uint64
	rpcErrors  atomic.Uint64
}

func TestLoadTransportStressConfigDefaults(t *testing.T) {
	t.Setenv("WK_TRANSPORT_STRESS", "")

	cfg := loadTransportStressConfig(t)

	if cfg.Enabled {
		t.Fatal("expected stress test to be disabled by default")
	}
	if cfg.Duration != 5*time.Second {
		t.Fatalf("Duration = %s, want %s", cfg.Duration, 5*time.Second)
	}
	if cfg.P99Budget != 20*time.Millisecond {
		t.Fatalf("P99Budget = %s, want %s", cfg.P99Budget, 20*time.Millisecond)
	}
	if cfg.RPCDelay != 2*time.Millisecond {
		t.Fatalf("RPCDelay = %s, want %s", cfg.RPCDelay, 2*time.Millisecond)
	}
}

func TestLoadTransportStressConfigRejectsNonPositiveDuration(t *testing.T) {
	t.Setenv("WK_TRANSPORT_STRESS_DURATION", "0s")

	_, err := parseTransportStressConfig()
	if err == nil {
		t.Fatal("expected non-positive duration to return an error")
	}
}

func TestSummarizeTransportLatencies(t *testing.T) {
	summary := summarizeTransportLatencies([]time.Duration{
		5 * time.Millisecond,
		1 * time.Millisecond,
		3 * time.Millisecond,
		9 * time.Millisecond,
	})

	if summary.Count != 4 {
		t.Fatalf("Count = %d, want 4", summary.Count)
	}
	if summary.P50 != 3*time.Millisecond {
		t.Fatalf("P50 = %s, want %s", summary.P50, 3*time.Millisecond)
	}
	if summary.P95 != 9*time.Millisecond {
		t.Fatalf("P95 = %s, want %s", summary.P95, 9*time.Millisecond)
	}
	if summary.P99 != 9*time.Millisecond {
		t.Fatalf("P99 = %s, want %s", summary.P99, 9*time.Millisecond)
	}
	if summary.Max != 9*time.Millisecond {
		t.Fatalf("Max = %s, want %s", summary.Max, 9*time.Millisecond)
	}
}

func TestRequireTransportStressEnabledWhenEnabled(t *testing.T) {
	requireTransportStressEnabled(t, transportStressConfig{Enabled: true})
}

func TestNewTransportStressHarnessStartsServerAndClients(t *testing.T) {
	h := newTransportStressHarness(t, transportStressConfig{RPCDelay: time.Millisecond})
	defer h.Close()

	if h.server.Listener() == nil {
		t.Fatal("expected active listener")
	}
	if h.raftClient == nil {
		t.Fatal("expected raft client to be initialized")
	}
	if h.rpcClient == nil {
		t.Fatal("expected rpc client to be initialized")
	}
}

func TestTransportStressHarnessSmoke(t *testing.T) {
	h := newTransportStressHarness(t, transportStressConfig{RPCDelay: time.Millisecond})
	defer h.Close()

	payload := make([]byte, 16)
	binary.BigEndian.PutUint64(payload[:8], uint64(time.Now().UnixNano()))
	binary.BigEndian.PutUint64(payload[8:], 1)

	if err := h.raftClient.Send(transportStressNodeID, 0, transportStressMsgType, payload); err != nil {
		t.Fatalf("raftClient.Send() error = %v", err)
	}
	resp, err := h.callRPC(context.Background(), []byte("ping"))
	if err != nil {
		t.Fatalf("callRPC() error = %v", err)
	}
	if string(resp) != "ok" {
		t.Fatalf("callRPC() resp = %q, want %q", resp, "ok")
	}

	requireEventually(t, func() bool {
		return len(h.raftStats.Snapshot()) == 1 && len(h.rpcStats.Snapshot()) == 1
	})
}

func TestTransportStressRaftSendUnderRPCLoad(t *testing.T) {
	cfg := loadTransportStressConfig(t)
	requireTransportStressEnabled(t, cfg)

	h := newTransportStressHarness(t, cfg)
	defer h.Close()

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	runTransportStressWorkers(ctx, t, h, cfg)

	raftSummary := summarizeTransportLatencies(h.raftStats.Snapshot())
	rpcSummary := summarizeTransportLatencies(h.rpcStats.Snapshot())

	if raftSummary.Count < 1000 {
		t.Fatalf("raft sample count = %d, want >= 1000", raftSummary.Count)
	}
	if rpcSummary.Count < 1000 {
		t.Fatalf("rpc sample count = %d, want >= 1000", rpcSummary.Count)
	}
	if h.raftErrors.Load() != 0 {
		t.Fatalf("raft errors = %d, want 0", h.raftErrors.Load())
	}
	if h.rpcErrors.Load() != 0 {
		t.Fatalf("rpc errors = %d, want 0", h.rpcErrors.Load())
	}
	if raftSummary.P99 > cfg.P99Budget {
		t.Fatalf("raft p99 = %s, want <= %s", raftSummary.P99, cfg.P99Budget)
	}

	t.Logf(
		"transport stress metrics: seed=%d duration=%s raft_workers=%d rpc_workers=%d raft_count=%d raft_p50=%s raft_p95=%s raft_p99=%s raft_max=%s rpc_count=%d rpc_p50=%s rpc_p95=%s rpc_p99=%s rpc_max=%s",
		cfg.Seed,
		cfg.Duration,
		cfg.RaftWorkers,
		cfg.RPCWorkers,
		raftSummary.Count,
		raftSummary.P50,
		raftSummary.P95,
		raftSummary.P99,
		raftSummary.Max,
		rpcSummary.Count,
		rpcSummary.P50,
		rpcSummary.P95,
		rpcSummary.P99,
		rpcSummary.Max,
	)
}

func requireTransportStressEnabled(t *testing.T, cfg transportStressConfig) {
	t.Helper()
	if !cfg.Enabled {
		t.Skip("set WK_TRANSPORT_STRESS=1 to enable transport stress test")
	}
}

func newTransportStressHarness(t *testing.T, cfg transportStressConfig) *transportStressHarness {
	t.Helper()

	server := NewServer()
	raftStats := &transportSampleCollector{}
	rpcStats := &transportSampleCollector{}

	server.Handle(transportStressMsgType, func(body []byte) {
		if len(body) < 16 {
			return
		}
		sentAt := time.Unix(0, int64(binary.BigEndian.Uint64(body[:8])))
		raftStats.Record(time.Since(sentAt))
	})
	server.HandleRPC(func(ctx context.Context, body []byte) ([]byte, error) {
		if cfg.RPCDelay > 0 {
			time.Sleep(cfg.RPCDelay)
		}
		return []byte("ok"), nil
	})
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("server.Start() error = %v", err)
	}

	discovery := staticDiscovery{addrs: map[NodeID]string{
		transportStressNodeID: server.Listener().Addr().String(),
	}}
	raftPool := NewPool(PoolConfig{
		Discovery:   discovery,
		Size:        1,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{1024, 1024, 1024},
		DefaultPri:  PriorityRaft,
	})
	rpcPool := NewPool(PoolConfig{
		Discovery:   discovery,
		Size:        1,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{1024, 1024, 1024},
		DefaultPri:  PriorityRPC,
	})

	return &transportStressHarness{
		server:     server,
		raftClient: NewClient(raftPool),
		rpcClient:  NewClient(rpcPool),
		raftStats:  raftStats,
		rpcStats:   rpcStats,
	}
}

func (h *transportStressHarness) Close() {
	if h == nil {
		return
	}
	if h.raftClient != nil {
		h.raftClient.Stop()
	}
	if h.rpcClient != nil {
		h.rpcClient.Stop()
	}
	if h.server != nil {
		h.server.Stop()
	}
}

func (h *transportStressHarness) callRPC(ctx context.Context, payload []byte) ([]byte, error) {
	startedAt := time.Now()
	resp, err := h.rpcClient.RPC(ctx, transportStressNodeID, 0, payload)
	if err != nil {
		return nil, err
	}
	h.rpcStats.Record(time.Since(startedAt))
	return resp, nil
}

func (c *transportSampleCollector) Record(latency time.Duration) {
	c.mu.Lock()
	c.latencies = append(c.latencies, latency)
	c.mu.Unlock()
	c.count.Add(1)
}

func (c *transportSampleCollector) Snapshot() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]time.Duration(nil), c.latencies...)
}

func (c *transportSampleCollector) Count() uint64 {
	return c.count.Load()
}

func runTransportStressWorkers(ctx context.Context, t *testing.T, h *transportStressHarness, cfg transportStressConfig) {
	t.Helper()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg      sync.WaitGroup
		errOnce sync.Once
		runErr  error
	)
	recordErr := func(err error) {
		if err == nil {
			return
		}
		errOnce.Do(func() {
			runErr = err
			cancel()
		})
	}

	for worker := 0; worker < cfg.RaftWorkers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			seq := uint64(worker + 1)
			for {
				if err := waitForTransportStressCapacity(ctx, h, uint64(cfg.RaftWorkers*128)); err != nil {
					return
				}

				select {
				case <-ctx.Done():
					return
				default:
				}

				payload := encodeTransportStressPayload(seq)
				if err := h.raftClient.Send(transportStressNodeID, 0, transportStressMsgType, payload); err != nil {
					if errors.Is(err, ErrQueueFull) {
						time.Sleep(200 * time.Microsecond)
						continue
					}
					h.raftErrors.Add(1)
					recordErr(fmt.Errorf("raft worker %d send: %w", worker, err))
					return
				}
				h.raftSent.Add(1)
				seq += uint64(cfg.RaftWorkers)
			}
		}(worker)
	}

	for worker := 0; worker < cfg.RPCWorkers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				resp, err := h.callRPC(ctx, []byte("ping"))
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					h.rpcErrors.Add(1)
					recordErr(fmt.Errorf("rpc worker %d rpc: %w", worker, err))
					return
				}
				if string(resp) != "ok" {
					h.rpcErrors.Add(1)
					recordErr(fmt.Errorf("rpc worker %d response = %q, want %q", worker, resp, "ok"))
					return
				}
			}
		}(worker)
	}

	wg.Wait()

	if runErr != nil {
		t.Fatal(runErr)
	}
}

func encodeTransportStressPayload(sequence uint64) []byte {
	payload := make([]byte, 16)
	binary.BigEndian.PutUint64(payload[:8], uint64(time.Now().UnixNano()))
	binary.BigEndian.PutUint64(payload[8:], sequence)
	return payload
}

func waitForTransportStressCapacity(ctx context.Context, h *transportStressHarness, maxInflight uint64) error {
	if maxInflight == 0 {
		return nil
	}
	for {
		sent := h.raftSent.Load()
		received := h.raftStats.Count()
		if sent <= received || sent-received < maxInflight {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Microsecond):
		}
	}
}

func loadTransportStressConfig(t *testing.T) transportStressConfig {
	t.Helper()

	cfg, err := parseTransportStressConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func parseTransportStressConfig() (transportStressConfig, error) {
	enabled, err := envBool(transportStressEnv, false)
	if err != nil {
		return transportStressConfig{}, err
	}
	duration, err := envDuration(transportStressDurationEnv, 5*time.Second)
	if err != nil {
		return transportStressConfig{}, err
	}
	raftWorkers, err := envInt(transportStressRaftWorkersEnv, maxInt(2, runtime.GOMAXPROCS(0)/2))
	if err != nil {
		return transportStressConfig{}, err
	}
	rpcWorkers, err := envInt(transportStressRPCWorkersEnv, maxInt(2, runtime.GOMAXPROCS(0)))
	if err != nil {
		return transportStressConfig{}, err
	}
	rpcDelay, err := envDuration(transportStressRPCDelayEnv, 2*time.Millisecond)
	if err != nil {
		return transportStressConfig{}, err
	}
	seed, err := envInt64(transportStressSeedEnv, 20260411)
	if err != nil {
		return transportStressConfig{}, err
	}
	p99Budget, err := envDuration(transportStressP99BudgetEnv, 20*time.Millisecond)
	if err != nil {
		return transportStressConfig{}, err
	}

	cfg := transportStressConfig{
		Enabled:     enabled,
		Duration:    duration,
		RaftWorkers: raftWorkers,
		RPCWorkers:  rpcWorkers,
		RPCDelay:    rpcDelay,
		Seed:        seed,
		P99Budget:   p99Budget,
	}

	if cfg.Duration <= 0 {
		return transportStressConfig{}, errInvalidStressValue(transportStressDurationEnv, "> 0", cfg.Duration)
	}
	if cfg.RaftWorkers <= 0 {
		return transportStressConfig{}, errInvalidStressValue(transportStressRaftWorkersEnv, "> 0", cfg.RaftWorkers)
	}
	if cfg.RPCWorkers <= 0 {
		return transportStressConfig{}, errInvalidStressValue(transportStressRPCWorkersEnv, "> 0", cfg.RPCWorkers)
	}
	if cfg.RPCDelay < 0 {
		return transportStressConfig{}, errInvalidStressValue(transportStressRPCDelayEnv, ">= 0", cfg.RPCDelay)
	}
	if cfg.P99Budget <= 0 {
		return transportStressConfig{}, errInvalidStressValue(transportStressP99BudgetEnv, "> 0", cfg.P99Budget)
	}
	return cfg, nil
}

func summarizeTransportLatencies(latencies []time.Duration) transportLatencySummary {
	if len(latencies) == 0 {
		return transportLatencySummary{}
	}

	sorted := append([]time.Duration(nil), latencies...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	return transportLatencySummary{
		Count: len(sorted),
		P50:   percentileTransportDuration(sorted, 0.50),
		P95:   percentileTransportDuration(sorted, 0.95),
		P99:   percentileTransportDuration(sorted, 0.99),
		Max:   sorted[len(sorted)-1],
	}
}

func percentileTransportDuration(sorted []time.Duration, pct float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(sorted))*pct)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func envBool(key string, fallback bool) (bool, error) {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func envInt(key string, fallback int) (int, error) {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func envInt64(key string, fallback int64) (int64, error) {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func errInvalidStressValue(key string, want string, got any) error {
	return &transportStressConfigError{key: key, want: want, got: got}
}

type transportStressConfigError struct {
	key  string
	want string
	got  any
}

func (e *transportStressConfigError) Error() string {
	return fmt.Sprintf("%s must be %s, got %v", e.key, e.want, e.got)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
