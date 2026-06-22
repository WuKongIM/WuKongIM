package app

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	accessmanager "github.com/WuKongIM/WuKongIM/internalv2/access/manager"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerClusterMonitorProviderReturnsDisabledWhenNotEnabled(t *testing.T) {
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: false,
		Now:     func() time.Time { return time.Unix(1781767200, 0).UTC() },
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.RealtimeMonitorStatusPrometheusDisabled {
		t.Fatalf("Status = %q, want %q", resp.Status, accessmanager.RealtimeMonitorStatusPrometheusDisabled)
	}
	if resp.Sources.Prometheus.Enabled {
		t.Fatalf("Prometheus.Enabled = true, want false")
	}
	if len(resp.Cards) != 0 || len(resp.Snapshot) != 0 {
		t.Fatalf("disabled response cards/snapshot = %d/%d, want empty", len(resp.Cards), len(resp.Snapshot))
	}
}

func TestManagerClusterMonitorProviderMapsPrometheusAndControlSnapshot(t *testing.T) {
	var calls atomic.Int64
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1/query_range" {
			t.Fatalf("path = %s, want /api/v1/query_range", r.URL.Path)
		}
		if r.URL.Query().Get("step") != "20" {
			t.Fatalf("step = %q, want 20", r.URL.Query().Get("step"))
		}
		query := r.URL.Query().Get("query")
		if !strings.Contains(query, "wukongim_") {
			t.Fatalf("query = %q, want wukongim metric", query)
		}
		queries = append(queries, query)
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1781767200,"12.5"],[1781767220,"15"]]}]}}`))
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
		Control: managerClusterControlReaderFake{},
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.RealtimeMonitorStatusReady {
		t.Fatalf("Status = %q, want ready; sources=%#v", resp.Status, resp.Sources)
	}
	wantCalls := len(managerMonitorMetricDefinitions()) + len(managerClusterMonitorMetricDefinitions())
	if calls.Load() != int64(wantCalls) {
		t.Fatalf("prometheus calls = %d, want %d", calls.Load(), wantCalls)
	}
	joinedQueries := strings.Join(queries, "\n")
	if !strings.Contains(joinedQueries, `wukongim_transport_sent_bytes_total{job="wukongimv2"}[1m]`) ||
		!strings.Contains(joinedQueries, `wukongim_transport_received_bytes_total{job="wukongimv2"}[1m]`) {
		t.Fatalf("queries = %q, want internal traffic rate windows filled", joinedQueries)
	}
	if !strings.Contains(joinedQueries, "wukongim_channelv2_active_runtimes") || !strings.Contains(joinedQueries, "vector(0)") {
		t.Fatalf("queries = %q, want active runtime zero fallback", joinedQueries)
	}
	if !strings.Contains(joinedQueries, `wukongim_slot_proposals_total{job="wukongimv2"}[1m]`) ||
		!strings.Contains(joinedQueries, `wukongim_slot_apply_gap{job="wukongimv2"}`) ||
		!strings.Contains(joinedQueries, `wukongim_slot_apply_duration_seconds_bucket{job="wukongimv2"}[1m]`) {
		t.Fatalf("queries = %#v, want propose rate, apply gap, and latency p99", queries)
	}
	if !strings.Contains(joinedQueries, `wukongim_node_cpu_percent{job="wukongimv2"}`) ||
		!strings.Contains(joinedQueries, `wukongim_node_memory_rss_bytes{job="wukongimv2"}`) ||
		!strings.Contains(joinedQueries, `wukongim_node_goroutines{job="wukongimv2"}`) {
		t.Fatalf("queries = %#v, want cpu, rss, and goroutine zero-fallback metrics", queries)
	}
	wantKeys := []string{
		"controllerProposeRate",
		"controllerApplyGap",
		"slotLeaderStability",
		"slotProposeRate",
		"slotApplyGap",
		"slotLatencyP99",
		"channelAppendLatencyP99",
		"activeChannels",
		"internalTraffic",
		"rpcSuccessRate",
		"rpcLatencyP95",
		"workqueuePressure",
		"nodeCpuPercent",
		"nodeMemoryRSS",
		"nodeGoroutines",
		"storageWriteP99",
	}
	if len(resp.Cards) != wantCalls {
		t.Fatalf("cards = %d, want %d", len(resp.Cards), wantCalls)
	}
	for _, want := range wantKeys {
		card := requireMonitorCardForTest(t, resp.Cards, want)
		if card.Source != accessmanager.RealtimeMonitorSourcePrometheus || !card.Available {
			t.Fatalf("card %q = %#v, want available prometheus card", want, card)
		}
	}
	controlCard := requireMonitorCardForTest(t, resp.Cards, "controllerProposeRate")
	if controlCard.Stage != accessmanager.RealtimeMonitorStageControlPlane || controlCard.Value != 15 {
		t.Fatalf("control card = %#v, want control-plane latest value 15", controlCard)
	}
	activeCard := requireMonitorCardForTest(t, resp.Cards, "activeChannels")
	if activeCard.Key != "activeChannels" || activeCard.Value != 15 || activeCard.Unit != "" {
		t.Fatalf("activeChannels card = %#v, want active channel count 15", activeCard)
	}
	if !resp.Sources.ControlSnapshot.Enabled {
		t.Fatalf("ControlSnapshot.Enabled = false, want true")
	}
	requireClusterSnapshotValue(t, resp.Snapshot, "nodesAlive", 2, accessmanager.RealtimeMonitorSourceControlSnapshot)
	requireClusterSnapshotValueWithUnit(t, resp.Snapshot, "slotsReady", (1.0/3.0)*100, "%", accessmanager.RealtimeMonitorSourceControlSnapshot)
	requireClusterSnapshotValue(t, resp.Snapshot, "controllerApplyGap", 15, accessmanager.RealtimeMonitorSourcePrometheus)
	requireClusterSnapshotValue(t, resp.Snapshot, "rpcErrorRate", 85, accessmanager.RealtimeMonitorSourcePrometheus)
	requireClusterSnapshotValue(t, resp.Snapshot, "queuePressure", 15, accessmanager.RealtimeMonitorSourcePrometheus)
	requireClusterSnapshotValue(t, resp.Snapshot, "storageWriteP99", 15, accessmanager.RealtimeMonitorSourcePrometheus)
}

func TestManagerClusterMonitorProviderIncludesAllNodeResourceStats(t *testing.T) {
	var nodeCPUQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if strings.Contains(query, "wukongim_node_cpu_percent") {
			nodeCPUQuery = query
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"node_id":"1","node_name":"node-1"},"values":[[1781767200,"12.5"],[1781767220,"15"]]},{"metric":{"node_id":"2","node_name":"node-2"},"values":[[1781767200,"32.5"],[1781767220,"40"]]}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1781767200,"12.5"],[1781767220,"15"]]}]}}`))
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
		Control: managerClusterControlReaderFake{},
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if nodeCPUQuery == "" {
		t.Fatal("node cpu query was not issued")
	}
	if strings.Contains(nodeCPUQuery, "max(") {
		t.Fatalf("node cpu query = %q, want raw per-node series", nodeCPUQuery)
	}
	var cpuCard accessmanager.RealtimeMonitorCard
	for _, card := range resp.Cards {
		if card.Key == "nodeCpuPercent" {
			cpuCard = card
			break
		}
	}
	if cpuCard.Key == "" {
		t.Fatalf("nodeCpuPercent card missing: %#v", resp.Cards)
	}
	if cpuCard.Value != 40 {
		t.Fatalf("node cpu value = %v, want highest current node value 40", cpuCard.Value)
	}
	if len(cpuCard.Series) != 4 {
		t.Fatalf("node cpu series = %#v, want two points per node", cpuCard.Series)
	}
	requireClusterCardPoint(t, cpuCard, 1781767200000, "node-1", 12.5)
	requireClusterCardPoint(t, cpuCard, 1781767220000, "node-1", 15)
	requireClusterCardPoint(t, cpuCard, 1781767200000, "node-2", 32.5)
	requireClusterCardPoint(t, cpuCard, 1781767220000, "node-2", 40)
	requireClusterCardStat(t, cpuCard, "node-1", 15, "%")
	requireClusterCardStat(t, cpuCard, "node-2", 40, "%")
}

func TestManagerClusterMonitorProviderFiltersPromQLAndControlSnapshotByNodeID(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		queries = append(queries, query)
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1781767200,"12.5"],[1781767220,"15"]]}]}}`))
	}))
	defer server.Close()
	control := &managerClusterControlReaderSpy{}
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
		Control: control,
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
		NodeID: 2,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if resp.Scope.NodeID != 2 {
		t.Fatalf("Scope.NodeID = %d, want 2", resp.Scope.NodeID)
	}
	joinedQueries := strings.Join(queries, "\n")
	for _, want := range []string{
		`wukongim_controller_decisions_total{job="wukongimv2",node_id="2"}[1m]`,
		`wukongim_transport_rpc_total{job="wukongimv2",node_id="2",result="ok"}[1m]`,
		`wukongim_transport_rpc_total{job="wukongimv2",node_id="2"}[1m]`,
	} {
		if !strings.Contains(joinedQueries, want) {
			t.Fatalf("promql queries missing %q: %s", want, joinedQueries)
		}
	}
	for _, forbidden := range []string{
		`wukongim_controller_decisions_total[1m]`,
		`wukongim_controller_decisions_total{node_id="2"}[1m]`,
		`wukongim_transport_rpc_total{node_id="2",result="ok"}[1m]`,
		`wukongim_transport_rpc_total{result="ok"}[1m]`,
	} {
		if strings.Contains(joinedQueries, forbidden) {
			t.Fatalf("promql queries still contain unfiltered selector %q: %s", forbidden, joinedQueries)
		}
	}
	if len(control.listSlotsOptions) != 1 {
		t.Fatalf("ListSlots calls = %d, want 1", len(control.listSlotsOptions))
	}
	if control.listSlotsOptions[0].NodeID != 2 {
		t.Fatalf("ListSlots NodeID = %d, want 2", control.listSlotsOptions[0].NodeID)
	}
	requireClusterSnapshotValue(t, resp.Snapshot, "nodesAlive", 1, accessmanager.RealtimeMonitorSourceControlSnapshot)
}

func TestManagerClusterMonitorProviderReturnsPartialForMissingMetric(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 2 {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1781767200,"12.5"],[1781767220,"15"]]}]}}`))
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
		Control: managerClusterControlReaderFake{},
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.RealtimeMonitorStatusPartial {
		t.Fatalf("Status = %q, want partial", resp.Status)
	}
	if resp.Sources.Prometheus.Error == "" {
		t.Fatalf("Prometheus source error is empty, want partial source error")
	}
	var unavailable int
	for _, card := range resp.Cards {
		if !card.Available {
			unavailable++
			if card.Error == "" {
				t.Fatalf("unavailable card = %#v, want card-local error", card)
			}
		}
	}
	if unavailable != 1 {
		t.Fatalf("unavailable cards = %d, want 1; cards=%#v", unavailable, resp.Cards)
	}
}

func TestManagerClusterMonitorProviderReturnsPartialWhenControlSnapshotUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		control managerClusterControlReader
		wantErr string
	}{
		{name: "absent", control: nil, wantErr: "control snapshot reader is not configured"},
		{name: "failure", control: managerClusterControlReaderFake{err: errors.New("snapshot failed")}, wantErr: "snapshot failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1781767200,"12.5"],[1781767220,"15"]]}]}}`))
			}))
			defer server.Close()
			provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
				Enabled: true,
				BaseURL: server.URL,
				Client:  server.Client(),
				Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
				Control: tc.control,
			})

			resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
				Window: 15 * time.Minute,
				Step:   20 * time.Second,
			})

			if err != nil {
				t.Fatalf("RealtimeMonitor() error = %v", err)
			}
			if resp.Status != accessmanager.RealtimeMonitorStatusPartial {
				t.Fatalf("Status = %q, want partial", resp.Status)
			}
			if resp.Sources.ControlSnapshot.Enabled {
				t.Fatalf("ControlSnapshot.Enabled = true, want false")
			}
			if !strings.Contains(resp.Sources.ControlSnapshot.Error, tc.wantErr) {
				t.Fatalf("ControlSnapshot.Error = %q, want %q", resp.Sources.ControlSnapshot.Error, tc.wantErr)
			}
			if len(resp.Cards) == 0 || !resp.Cards[0].Available {
				t.Fatalf("cards = %#v, want prometheus cards still available", resp.Cards)
			}
		})
	}
}

func requireClusterSnapshotValue(t *testing.T, snapshot []accessmanager.RealtimeMonitorSnapshotEntry, key string, want float64, source string) {
	t.Helper()
	for _, item := range snapshot {
		if item.Key == key {
			if item.Value != want || item.Source != source {
				t.Fatalf("snapshot[%s] = %#v, want value %v source %s", key, item, want, source)
			}
			return
		}
	}
	t.Fatalf("snapshot missing key %s: %#v", key, snapshot)
}

func requireClusterSnapshotValueWithUnit(t *testing.T, snapshot []accessmanager.RealtimeMonitorSnapshotEntry, key string, want float64, unit string, source string) {
	t.Helper()
	for _, item := range snapshot {
		if item.Key == key {
			if math.Abs(item.Value-want) > 1e-9 || item.Unit != unit || item.Source != source {
				t.Fatalf("snapshot[%s] = %#v, want value %v unit %q source %s", key, item, want, unit, source)
			}
			return
		}
	}
	t.Fatalf("snapshot missing key %s: %#v", key, snapshot)
}

func requireClusterCardStat(t *testing.T, card accessmanager.RealtimeMonitorCard, label string, want float64, unit string) {
	t.Helper()
	for _, stat := range card.Stats {
		if stat.Label == label {
			if math.Abs(stat.Value-want) > 1e-9 || stat.Unit != unit {
				t.Fatalf("stat %q = %#v, want value %v unit %q", label, stat, want, unit)
			}
			return
		}
	}
	t.Fatalf("card %s missing stat label %q: %#v", card.Key, label, card.Stats)
}

func requireClusterCardPoint(t *testing.T, card accessmanager.RealtimeMonitorCard, timestamp int64, label string, want float64) {
	t.Helper()
	for _, point := range card.Series {
		if point.Timestamp == timestamp && point.Label == label {
			if math.Abs(point.Value-want) > 1e-9 {
				t.Fatalf("point %s/%d = %#v, want %v", label, timestamp, point, want)
			}
			return
		}
	}
	t.Fatalf("card %s missing point label %q timestamp %d: %#v", card.Key, label, timestamp, card.Series)
}

type managerClusterControlReaderFake struct {
	err error
}

func (f managerClusterControlReaderFake) ListNodes(context.Context) (managementusecase.NodeList, error) {
	if f.err != nil {
		return managementusecase.NodeList{}, f.err
	}
	return managementusecase.NodeList{
		GeneratedAt:        time.Unix(1781767240, 0).UTC(),
		ControllerLeaderID: 1,
		Items: []managementusecase.Node{{
			NodeID: 1,
			Status: "alive",
		}, {
			NodeID: 2,
			Status: "alive",
		}, {
			NodeID: 3,
			Status: "dead",
		}},
	}, nil
}

func (f managerClusterControlReaderFake) ListSlots(context.Context, managementusecase.ListSlotsOptions) ([]managementusecase.Slot, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []managementusecase.Slot{{
		State:   managementusecase.SlotState{Quorum: "ready", LeaderMatch: true},
		Runtime: managementusecase.SlotRuntime{PreferredLeaderID: 1, HasQuorum: true},
	}, {
		State:   managementusecase.SlotState{Quorum: "ready"},
		Runtime: managementusecase.SlotRuntime{HasQuorum: true},
	}, {
		State:   managementusecase.SlotState{Quorum: "lost"},
		Runtime: managementusecase.SlotRuntime{PreferredLeaderID: 3},
	}}, nil
}

type managerClusterControlReaderSpy struct {
	listSlotsOptions []managementusecase.ListSlotsOptions
}

func (f *managerClusterControlReaderSpy) ListNodes(context.Context) (managementusecase.NodeList, error) {
	return managementusecase.NodeList{
		GeneratedAt:        time.Unix(1781767240, 0).UTC(),
		ControllerLeaderID: 1,
		Items: []managementusecase.Node{{
			NodeID: 1,
			Status: "alive",
		}, {
			NodeID: 2,
			Status: "alive",
		}, {
			NodeID: 3,
			Status: "dead",
		}},
	}, nil
}

func (f *managerClusterControlReaderSpy) ListSlots(_ context.Context, options managementusecase.ListSlotsOptions) ([]managementusecase.Slot, error) {
	f.listSlotsOptions = append(f.listSlotsOptions, options)
	return []managementusecase.Slot{{
		State:   managementusecase.SlotState{Quorum: "ready", LeaderMatch: true},
		Runtime: managementusecase.SlotRuntime{PreferredLeaderID: 2, HasQuorum: true},
	}}, nil
}
