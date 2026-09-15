//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	"github.com/WuKongIM/WuKongIM/internal/runtime/messageupdates"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// pressureDispatch gates only the test's notification consumer. Released calls
// use the real usecase, authoritative subscriber pages and durable progress CAS.
type pressureDispatch struct {
	messageupdates.Dispatcher
	blocked      atomic.Bool
	attempts     atomic.Int64
	pages        atomic.Int64
	calls        atomic.Int64
	failures     atomic.Int64
	dispatchNS   atomic.Int64
	largeStarted chan struct{}
	once         sync.Once
}

func (d *pressureDispatch) DispatchMessageUpdate(ctx context.Context, task metadb.MessageUpdate) (bool, error) {
	if d.blocked.Load() {
		d.attempts.Add(1)
		<-ctx.Done()
		return false, ctx.Err()
	}
	started := time.Now()
	more, err := d.Dispatcher.DispatchMessageUpdate(ctx, task)
	d.calls.Add(1)
	d.dispatchNS.Add(int64(time.Since(started)))
	if err != nil {
		d.failures.Add(1)
	}
	if task.ChannelID == "pressure-large" && err == nil && more {
		d.pages.Add(1)
		d.once.Do(func() { close(d.largeStarted) })
	}
	return more, err
}

// This opt-in fixture measures three real cluster nodes in one test process.
// HTTP handler timings exclude the client network; pending completion is not
// online client latency. The separate process E2E owns SIGKILL/recovery coverage.
func TestMessageUpdateThreeNodePressure(t *testing.T) {
	if os.Getenv("WK_MESSAGE_UPDATE_PRESSURE") != "1" {
		t.Skip("explicit bounded pressure validation")
	}
	reportPath := os.Getenv("WK_MESSAGE_UPDATE_PRESSURE_REPORT")
	if reportPath == "" {
		t.Fatal("report path required")
	}
	report := map[string]any{"complete": false, "hash_slots": 256, "physical_slots": 8, "replicas": 3, "go": runtime.Version(), "gomaxprocs": runtime.GOMAXPROCS(0), "logical_cpus": runtime.NumCPU()}
	defer func() {
		b, e := json.MarshalIndent(report, "", "  ")
		if e == nil {
			e = os.WriteFile(reportPath, append(b, '\n'), 0644)
		}
		if e != nil {
			t.Error(e)
		}
	}()
	voters := make([]cluster.ControlVoter, 3)
	for i := range voters {
		voters[i] = cluster.ControlVoter{NodeID: uint64(i + 1), Addr: freeSendackSmokeTCPAddr(t)}
	}
	var apps []*App
	var nodes []*cluster.Node
	var dispatchers []*pressureDispatch
	defer func() {
		var stats []map[string]any
		for i, d := range dispatchers {
			stats = append(stats, map[string]any{"node": i + 1, "calls": d.calls.Load(), "failures": d.failures.Load(), "dispatch_seconds": float64(d.dispatchNS.Load()) / float64(time.Second)})
		}
		report["dispatch_stats"] = stats
	}()
	defer t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		done := make(chan error, len(apps))
		for _, a := range apps {
			go func() { done <- a.Stop(ctx) }()
		}
		for range apps {
			if e := <-done; e != nil {
				t.Error(e)
			}
		}
	})
	for _, v := range voters {
		cfg := singleNodeClusterAppConfig(t)
		cfg.NodeID, cfg.Cluster.NodeID, cfg.Cluster.ListenAddr = v.NodeID, v.NodeID, v.Addr
		cfg.Cluster.Control.Voters = voters
		cfg.Cluster.Slots.HashSlotCount, cfg.Cluster.Slots.InitialSlotCount = 256, 8
		cfg.Cluster.Slots.ReplicaCount, cfg.Cluster.Channel.ReplicaCount = 3, 3
		cfg.API.ListenAddr = "127.0.0.1:0"
		a, e := New(cfg, WithLogger(wklog.NewNop()))
		if e != nil {
			t.Fatal(e)
		}
		d := &pressureDispatch{Dispatcher: a.messages, largeStarted: make(chan struct{})}
		a.messageUpdateWorker = messageupdates.New(a.cluster.(messageupdates.Source), d, a.goroutines, nil)
		apps = append(apps, a)
		nodes = append(nodes, a.cluster.(*cluster.Node))
		dispatchers = append(dispatchers, d)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	done := make(chan error, 3)
	for _, a := range apps {
		go func() { done <- a.Start(ctx) }()
	}
	for range apps {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	waitAppClusterSnapshotsConverge(t, nodes)
	for i, n := range nodes {
		waitSingleNodeClusterNodeSchedulable(t, n, uint64(i+1))
	}
	handlers := make([]http.Handler, 3)
	for i, a := range apps {
		handlers[i] = a.api.(*accessapi.Server).Handler()
	}
	call := func(node int, path string, body any) ([]byte, error) {
		b, e := json.Marshal(body)
		if e != nil {
			return nil, e
		}
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b)).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handlers[node].ServeHTTP(w, r)
		if w.Code != 200 {
			return nil, fmt.Errorf("%s status=%d body=%.512s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes(), nil
	}
	create := func(channel string, subscribers []string) {
		t.Helper()
		if _, e := call(0, "/channel", map[string]any{"channel_id": channel, "channel_type": 2, "subscribers": subscribers}); e != nil {
			t.Fatal(e)
		}
	}
	send := func(channel string, index int) (uint64, error) {
		b, e := call(0, "/message/send", map[string]any{"from_uid": "pressure-author", "channel_id": channel, "channel_type": 2, "client_msg_no": fmt.Sprintf("%s-%d", channel, index), "payload": "b2xk"})
		if e != nil {
			return 0, e
		}
		var out struct {
			ID uint64 `json:"message_id"`
		}
		e = json.Unmarshal(b, &out)
		if e == nil && out.ID == 0 {
			e = fmt.Errorf("invalid send: %s", b)
		}
		return out.ID, e
	}
	edit := func(channel string, id uint64, version int) (float64, error) {
		start := time.Now()
		_, e := call(0, "/message/update", map[string]any{"login_uid": "pressure-author", "channel_id": channel, "channel_type": 2, "message_id": fmt.Sprint(id), "expected_content_epoch": "0", "expected_version": fmt.Sprint(version), "request_id": fmt.Sprintf("pressure-%d-%d", id, version), "payload": "bmV3"})
		return float64(time.Since(start)) / float64(time.Millisecond), e
	}
	pending := func(channel string, ids []uint64) (int, error) {
		count := 0
		for offset := 0; offset < len(ids); offset += 128 {
			end := min(offset+128, len(ids))
			pages, e := nodes[0].ReadMessageUpdatesBatch(ctx, []metadb.MessageUpdateRead{{ChannelID: channel, ChannelType: 2, IDs: ids[offset:end], IncludePending: true}})
			if e != nil {
				return 0, e
			}
			if len(pages) != 1 {
				return 0, fmt.Errorf("missing pending response")
			}
			count += len(pages[0].Updates)
		}
		return count, nil
	}
	drain := func(channel string, ids []uint64, timeout time.Duration) time.Duration {
		t.Helper()
		start := time.Now()
		var curve []map[string]any
		nextSample := time.Duration(0)
		defer func() {
			if channel == "pressure-overflow" {
				report["overflow_backlog"] = curve
			}
		}()
		for time.Since(start) < timeout {
			n, e := pending(channel, ids)
			if e != nil {
				t.Fatal(e)
			}
			elapsed := time.Since(start)
			if elapsed >= nextSample || n == 0 {
				curve = append(curve, map[string]any{"seconds": elapsed.Seconds(), "pending": n})
				nextSample = elapsed + 5*time.Second
				if channel == "pressure-overflow" {
					t.Logf("backlog elapsed=%.1fs pending=%d", elapsed.Seconds(), n)
				}
			}
			if n == 0 {
				return time.Since(start)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
		t.Fatalf("pending did not drain: %s", channel)
		return 0
	}
	parallel := func(n, concurrency int, fn func(int) error) {
		t.Helper()
		jobs := make(chan int, n)
		errs := make(chan error, concurrency)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			jobs <- i
		}
		close(jobs)
		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobs {
					if e := fn(j); e != nil {
						errs <- e
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Fatal(e)
		}
	}
	quantiles := func(samples []float64) map[string]any {
		v := append([]float64(nil), samples...)
		sort.Float64s(v)
		return map[string]any{"samples": len(v), "p50_ms": v[int(math.Ceil(float64(len(v))*.50))-1], "p95_ms": v[int(math.Ceil(float64(len(v))*.95))-1], "p99_ms": v[int(math.Ceil(float64(len(v))*.99))-1], "max_ms": v[len(v)-1]}
	}
	memory := func() map[string]any {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return map[string]any{"heap_alloc_bytes": m.HeapAlloc, "heap_inuse_bytes": m.HeapInuse, "total_alloc_bytes": m.TotalAlloc, "gc_cycles": m.NumGC, "goroutines": runtime.NumGoroutine()}
	}
	const channels = 16
	ids := make([]uint64, channels)
	for i := range ids {
		name := fmt.Sprintf("pressure-%02d", i)
		create(name, []string{"pressure-author", "pressure-reader"})
		var e error
		ids[i], e = send(name, 0)
		if e != nil {
			t.Fatal(e)
		}
	}
	t.Log("healthy concurrent edits: 16 channels, 64 versions each")
	report["before_healthy"] = memory()
	samples := make([]float64, channels*64)
	start := time.Now()
	parallel(channels, channels, func(i int) error {
		for v := 0; v < 64; v++ {
			ms, e := edit(fmt.Sprintf("pressure-%02d", i), ids[i], v)
			if e != nil {
				return e
			}
			samples[i*64+v] = ms
		}
		return nil
	})
	report["healthy_api"] = quantiles(samples)
	report["healthy_seconds"] = time.Since(start).Seconds()
	report["after_healthy"] = memory()
	for i, id := range ids {
		drain(fmt.Sprintf("pressure-%02d", i), []uint64{id}, 30*time.Second)
	}
	const overflowCount = 1280
	create("pressure-overflow", []string{"pressure-author", "pressure-reader"})
	overflowIDs := make([]uint64, overflowCount)
	parallel(overflowCount, 16, func(i int) error { var e error; overflowIDs[i], e = send("pressure-overflow", i); return e })
	uniqueIDs := make(map[uint64]struct{}, len(overflowIDs))
	for _, id := range overflowIDs {
		uniqueIDs[id] = struct{}{}
	}
	if len(uniqueIDs) != overflowCount {
		t.Fatal("overflow requires distinct message IDs")
	}
	for _, d := range dispatchers {
		d.blocked.Store(true)
	}
	t.Log("notification consumers gated: 1280 distinct edit commits")
	samples = make([]float64, overflowCount)
	start = time.Now()
	parallel(overflowCount, 16, func(i int) error { var e error; samples[i], e = edit("pressure-overflow", overflowIDs[i], 0); return e })
	attempts := int64(0)
	for _, d := range dispatchers {
		attempts += d.attempts.Load()
	}
	remaining, e := pending("pressure-overflow", overflowIDs)
	if e != nil {
		t.Fatal(e)
	}
	report["overflow_api"] = quantiles(samples)
	report["overflow_commit_seconds"] = time.Since(start).Seconds()
	report["blocked_attempts"] = attempts
	report["pending_before_release"] = remaining
	// Every fast pop is a dispatcher attempt. Even subtracting attempts made by
	// all three workers, these distinct commits cannot fit the origin's 1024 map.
	minimumOverflow := overflowCount - int(attempts) - 1024
	report["minimum_overflowed_identities"] = minimumOverflow
	for _, d := range dispatchers {
		d.blocked.Store(false)
	}
	if minimumOverflow <= 0 || remaining != overflowCount {
		t.Fatalf("overflow not established: attempts=%d pending=%d", attempts, remaining)
	}
	overflowDrain := drain("pressure-overflow", overflowIDs, 180*time.Second)
	report["overflow_drain_seconds"] = overflowDrain.Seconds()
	report["overflow_within_90s"] = overflowDrain <= 90*time.Second
	report["after_overflow"] = memory()
	t.Log("100000-member group fairness against 16 small channels")
	subscribers := make([]string, 100000)
	subscribers[0] = "pressure-author"
	for i := 1; i < len(subscribers); i++ {
		subscribers[i] = fmt.Sprintf("pressure-member-%06d", i)
	}
	seedStarted := time.Now()
	// Isolate notification paging from UID conversation-directory construction.
	// The author joins through HTTP; remaining offline recipients are persisted
	// through the real authority-routed, counted Slot mutation, never a fake list.
	create("pressure-large", subscribers[:1])
	largeID, e := send("pressure-large", 0)
	if e != nil {
		t.Fatal(e)
	}
	for offset := 1; offset < len(subscribers); offset += 1000 {
		end := min(offset+1000, len(subscribers))
		result, err := nodes[0].AddChannelSubscribersCounted(ctx, "pressure-large", 2, subscribers[offset:end], uint64(offset+1))
		if err != nil {
			t.Fatal(err)
		}
		if result.ChangedCount != end-offset {
			t.Fatalf("subscriber seed changed %d, want %d", result.ChangedCount, end-offset)
		}
	}
	report["subscriber_seed_seconds"] = time.Since(seedStarted).Seconds()
	report["subscriber_seed_mode"] = "HTTP author membership plus authority-routed counted subscriber batches; offline recipient UID memberships omitted"
	t.Logf("100000 persisted subscribers prepared in %.2fs", time.Since(seedStarted).Seconds())
	start = time.Now()
	if _, e = edit("pressure-large", largeID, 0); e != nil {
		t.Fatal(e)
	}
	// Wait for an actual incomplete authoritative subscriber page before small
	// edits. No artificial delay is applied to either large or small dispatch.
	largeBegan := false
	deadline := time.Now().Add(15 * time.Second)
	for !largeBegan && time.Now().Before(deadline) {
		for _, d := range dispatchers {
			select {
			case <-d.largeStarted:
				largeBegan = true
			default:
			}
		}
		if !largeBegan {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if !largeBegan {
		t.Fatal("large group did not paginate")
	}
	samples = make([]float64, channels)
	parallel(channels, channels, func(i int) error {
		var e error
		samples[i], e = edit(fmt.Sprintf("pressure-%02d", i), ids[i], 64)
		return e
	})
	for i, id := range ids {
		drain(fmt.Sprintf("pressure-%02d", i), []uint64{id}, 30*time.Second)
	}
	smallDone := time.Since(start)
	largeRemaining, e := pending("pressure-large", []uint64{largeID})
	if e != nil {
		t.Fatal(e)
	}
	report["fairness_small_api"] = quantiles(samples)
	report["fairness_all_small_done_ms"] = float64(smallDone) / float64(time.Millisecond)
	report["large_pending_when_small_done"] = largeRemaining
	if largeRemaining != 1 {
		t.Fatal("large group completed before all small targets: fairness overlap not demonstrated")
	}
	drain("pressure-large", []uint64{largeID}, 240*time.Second)
	report["large_complete_ms"] = float64(time.Since(start)) / float64(time.Millisecond)
	pageCount := int64(0)
	for _, d := range dispatchers {
		pageCount += d.pages.Load()
	}
	report["large_incomplete_dispatch_calls"] = pageCount
	report["large_members"] = len(subscribers)
	report["online_clients"] = 0
	report["after_large"] = memory()
	report["complete"] = true
	t.Logf("pressure evidence: %s", reportPath)
}
