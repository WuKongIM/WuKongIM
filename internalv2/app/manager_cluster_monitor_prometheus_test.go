package app

import (
	"context"
	"errors"
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
	provider := newManagerClusterPrometheusMonitorProvider(managerClusterPrometheusMonitorOptions{
		Enabled: false,
		Now:     func() time.Time { return time.Unix(1781767200, 0).UTC() },
	})

	resp, err := provider.ClusterRealtimeMonitor(context.Background(), accessmanager.ClusterRealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("ClusterRealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.ClusterRealtimeMonitorStatusPrometheusDisabled {
		t.Fatalf("Status = %q, want %q", resp.Status, accessmanager.ClusterRealtimeMonitorStatusPrometheusDisabled)
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
	provider := newManagerClusterPrometheusMonitorProvider(managerClusterPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
		Control: managerClusterControlReaderFake{},
	})

	resp, err := provider.ClusterRealtimeMonitor(context.Background(), accessmanager.ClusterRealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("ClusterRealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.ClusterRealtimeMonitorStatusReady {
		t.Fatalf("Status = %q, want ready; sources=%#v", resp.Status, resp.Sources)
	}
	if calls.Load() != int64(len(managerClusterMonitorMetricDefinitions())) {
		t.Fatalf("prometheus calls = %d, want %d", calls.Load(), len(managerClusterMonitorMetricDefinitions()))
	}
	if len(queries) < 8 || !strings.Contains(queries[6], "wukongim_transport_sent_bytes_total[1m]") || !strings.Contains(queries[6], "wukongim_transport_received_bytes_total[1m]") {
		t.Fatalf("internalTraffic query = %q, want two rate windows filled", queries[6])
	}
	wantKeys := []string{
		"controllerProposeRate",
		"controllerApplyGap",
		"slotLeaderStability",
		"slotReplicaLagP99",
		"channelISRHealth",
		"channelAppendLatencyP99",
		"internalTraffic",
		"rpcSuccessRate",
		"rpcLatencyP95",
		"workqueuePressure",
		"storageWriteP99",
		"incidentRate",
	}
	if len(resp.Cards) != len(wantKeys) {
		t.Fatalf("cards = %d, want %d", len(resp.Cards), len(wantKeys))
	}
	for i, want := range wantKeys {
		if resp.Cards[i].Key != want {
			t.Fatalf("card[%d].Key = %q, want %q", i, resp.Cards[i].Key, want)
		}
		if resp.Cards[i].Source != accessmanager.ClusterRealtimeMonitorSourcePrometheus || !resp.Cards[i].Available {
			t.Fatalf("card[%d] = %#v, want available prometheus card", i, resp.Cards[i])
		}
	}
	if resp.Cards[0].Stage != accessmanager.ClusterRealtimeMonitorStageControlPlane || resp.Cards[0].Value != 15 {
		t.Fatalf("first card = %#v, want control-plane latest value 15", resp.Cards[0])
	}
	if !resp.Sources.ControlSnapshot.Enabled {
		t.Fatalf("ControlSnapshot.Enabled = false, want true")
	}
	requireClusterSnapshotValue(t, resp.Snapshot, "nodesAlive", 2, accessmanager.ClusterRealtimeMonitorSourceControlSnapshot)
	requireClusterSnapshotValue(t, resp.Snapshot, "slotsReady", 1, accessmanager.ClusterRealtimeMonitorSourceControlSnapshot)
	requireClusterSnapshotValue(t, resp.Snapshot, "controllerApplyGap", 15, accessmanager.ClusterRealtimeMonitorSourcePrometheus)
	requireClusterSnapshotValue(t, resp.Snapshot, "channelISRAnomalies", 15, accessmanager.ClusterRealtimeMonitorSourcePrometheus)
	requireClusterSnapshotValue(t, resp.Snapshot, "rpcErrorRate", 85, accessmanager.ClusterRealtimeMonitorSourcePrometheus)
	requireClusterSnapshotValue(t, resp.Snapshot, "queuePressure", 15, accessmanager.ClusterRealtimeMonitorSourcePrometheus)
	requireClusterSnapshotValue(t, resp.Snapshot, "storageWriteP99", 15, accessmanager.ClusterRealtimeMonitorSourcePrometheus)
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
	provider := newManagerClusterPrometheusMonitorProvider(managerClusterPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
		Control: managerClusterControlReaderFake{},
	})

	resp, err := provider.ClusterRealtimeMonitor(context.Background(), accessmanager.ClusterRealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("ClusterRealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.ClusterRealtimeMonitorStatusPartial {
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

func TestManagerClusterMonitorProviderKeepsTextOnlyIncidentStat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1781767200,"1"],[1781767220,"2"]]}]}}`))
	}))
	defer server.Close()
	provider := newManagerClusterPrometheusMonitorProvider(managerClusterPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
		Control: managerClusterControlReaderFake{},
	})

	resp, err := provider.ClusterRealtimeMonitor(context.Background(), accessmanager.ClusterRealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("ClusterRealtimeMonitor() error = %v", err)
	}
	incident := resp.Cards[len(resp.Cards)-1]
	if incident.Key != "incidentRate" {
		t.Fatalf("last card key = %q, want incidentRate", incident.Key)
	}
	var found bool
	for _, stat := range incident.Stats {
		if stat.Key == "topReason" && stat.Value == nil && stat.Text != "" {
			found = true
		}
		if stat.Key == "topReason" && stat.Value != nil {
			t.Fatalf("topReason stat = %#v, want Value nil", stat)
		}
	}
	if !found {
		t.Fatalf("incident stats = %#v, want text-only topReason", incident.Stats)
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
			provider := newManagerClusterPrometheusMonitorProvider(managerClusterPrometheusMonitorOptions{
				Enabled: true,
				BaseURL: server.URL,
				Client:  server.Client(),
				Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
				Control: tc.control,
			})

			resp, err := provider.ClusterRealtimeMonitor(context.Background(), accessmanager.ClusterRealtimeMonitorQuery{
				Window: 15 * time.Minute,
				Step:   20 * time.Second,
			})

			if err != nil {
				t.Fatalf("ClusterRealtimeMonitor() error = %v", err)
			}
			if resp.Status != accessmanager.ClusterRealtimeMonitorStatusPartial {
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

func requireClusterSnapshotValue(t *testing.T, snapshot []accessmanager.ClusterRealtimeMonitorSnapshotEntry, key string, want float64, source string) {
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
		Runtime: managementusecase.SlotRuntime{LeaderID: 1, HasQuorum: true},
	}, {
		State:   managementusecase.SlotState{Quorum: "ready"},
		Runtime: managementusecase.SlotRuntime{HasQuorum: true},
	}, {
		State:   managementusecase.SlotState{Quorum: "lost"},
		Runtime: managementusecase.SlotRuntime{LeaderID: 3},
	}}, nil
}
