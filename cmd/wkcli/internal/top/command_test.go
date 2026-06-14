package top

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
)

func TestTopOnceRendersHumanSnapshot(t *testing.T) {
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(sampleSnapshot())
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"--server", server.URL, "--once"})

	err := cmd.Execute()

	if err != nil {
		t.Fatalf("expected top once to run, got %v stderr %q", err, stderr.String())
	}
	if requestedPath != "/top/v1/snapshot" {
		t.Fatalf("expected snapshot path, got %q", requestedPath)
	}
	for _, want := range []string{"WuKongIM top", "VERDICT", "node-1", "channelv2"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output to contain %q, got %q", want, stdout.String())
		}
	}
}

func TestTopOnceRendersJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(sampleSnapshot())
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"--server", server.URL, "--once", "--json"})

	err := cmd.Execute()

	if err != nil {
		t.Fatalf("expected top once json to run, got %v stderr %q", err, stderr.String())
	}
	for _, want := range []string{`"nodes"`, `"node-1"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected JSON output to contain %q, got %q", want, stdout.String())
		}
	}
}

func sampleSnapshot() accessapi.TopSnapshot {
	return accessapi.TopSnapshot{
		Version:       "top/v1",
		Scope:         "local_node",
		GeneratedAt:   time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC),
		WindowSeconds: 10,
		Node: accessapi.TopNodeSnapshot{
			ID:               1,
			Name:             "node-1",
			Ready:            true,
			StateRevision:    12,
			SlotCount:        3,
			HashSlotCount:    64,
			ReadyParts:       map[string]bool{"cluster": true, "gateway": true},
			ControllerLeader: 1,
		},
		Verdict: accessapi.TopVerdict{
			Level:   "degraded",
			Summary: "channelv2 pressure is high",
			Reasons: []string{"channelv2 worker queue saturated"},
		},
		Traffic: &accessapi.TopTraffic{
			SendPerSec:       100.5,
			SendackPerSec:    99.5,
			AppendPerSec:     98.25,
			AppendP99MS:      45,
			DeliverPerSec:    120,
			FanoutRate:       1.2,
			SendackErrorRate: 0.01,
		},
		Clients: &accessapi.TopClients{
			Connections:           42,
			ConnectionsByProtocol: map[string]int64{"tcp": 40, "ws": 2},
			ClosePerSec:           0.5,
		},
		Pressure: &accessapi.TopPressure{
			OverallLevel:    "degraded",
			ComponentScores: map[string]float64{"channelv2": 0.91, "delivery": 0.35},
			Top: []accessapi.TopPressureItem{{
				Component: "channelv2",
				Pool:      "append",
				Queue:     "worker",
				Level:     "degraded",
				Score:     0.91,
				Depth:     91,
				Capacity:  100,
				WaitP99MS: 77,
				Hint:      "scale append workers",
			}},
		},
		ChannelV2: &accessapi.TopChannelV2{
			ActiveTotal:            9,
			ActiveLeader:           5,
			ActiveFollower:         4,
			FollowerParked:         1,
			ReactorMailboxDepthMax: 17,
			WorkerQueueDepthByPool: map[string]int64{"append": 91},
			WorkerInflightByPool:   map[string]int64{"append": 8},
			AppendP99MS:            77,
			HotStage:               "propose",
			StageP99MS:             map[string]float64{"propose": 77},
		},
		Sources: accessapi.TopSources{
			Collector:       accessapi.TopSourceStatus{Available: true, SampleCount: 10},
			ClusterSnapshot: accessapi.TopSourceStatus{Available: true},
			Metrics:         accessapi.TopMetricsSource{Enabled: false, Required: false},
		},
	}
}
