package top

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	contextcmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/context"
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

func TestTopOncePreservesServerQueryEndingSlashAndBasePath(t *testing.T) {
	var requestedPath string
	var token string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		token = r.URL.Query().Get("token")
		_ = json.NewEncoder(w).Encode(sampleSnapshot())
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"--server", server.URL + "/api?token=abc/", "--once"})

	err := cmd.Execute()

	if err != nil {
		t.Fatalf("expected top once to run, got %v stderr %q", err, stderr.String())
	}
	if requestedPath != "/api/top/v1/snapshot" {
		t.Fatalf("expected base path to be preserved, got %q", requestedPath)
	}
	if token != "abc/" {
		t.Fatalf("expected query value ending slash to be preserved, got %q", token)
	}
}

func TestTopWithoutOnceReturnsInteractiveMessageBeforeServerResolution(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs(nil)

	err := cmd.Execute()

	var exit command.Exit
	if !errors.As(err, &exit) {
		t.Fatalf("expected command exit, got %v", err)
	}
	if exit.Code != command.ExitConfig {
		t.Fatalf("expected config exit code %d, got %d", command.ExitConfig, exit.Code)
	}
	if exit.Message != "interactive top is not available yet; use --once" {
		t.Fatalf("expected interactive message, got %q", exit.Message)
	}
}

func TestTopContextDirIsResolvedAtExecution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(sampleSnapshot())
	}))
	defer server.Close()

	staleDir := t.TempDir()
	parsedDir := t.TempDir()
	if err := contextcmd.NewStore(parsedDir).Save(contextcmd.Context{
		Name:    "dev",
		Servers: []string{server.URL},
	}); err != nil {
		t.Fatalf("save context: %v", err)
	}

	var stdout, stderr bytes.Buffer
	contextDir := staleDir
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr, ContextDir: &contextDir})
	contextDir = parsedDir
	cmd.SetArgs([]string{"--context", "dev", "--once"})

	err := cmd.Execute()

	if err != nil {
		t.Fatalf("expected top once to use parsed context dir, got %v stderr %q", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "node-1") {
		t.Fatalf("expected context-backed snapshot output, got %q", stdout.String())
	}
}

func TestPressureSummaryBreaksComponentScoreTiesByName(t *testing.T) {
	for i := 0; i < 100; i++ {
		node := accessapi.TopSnapshot{
			Pressure: &accessapi.TopPressure{
				OverallLevel: "degraded",
				ComponentScores: map[string]float64{
					"delivery":  0.91,
					"channelv2": 0.91,
				},
			},
		}

		got := pressureSummary(node)

		if got != "channelv2 degraded 0.91" {
			t.Fatalf("expected lexical tie-breaker to choose channelv2, got %q", got)
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
