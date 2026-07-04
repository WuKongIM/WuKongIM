package top

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	contextcmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/context"
	accessapi "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/top/topapi"
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
	for _, want := range []string{"WuKongIM top", "VERDICT", "ALERTS", "critical", "append worker queue saturated", "CPU%", "MEM", "node-1", "12.50", "128MiB", "channelv2"} {
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

func TestTopAlertsRendersDetailedAlertEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(sampleSnapshot())
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"--server", server.URL, "--alerts"})

	err := cmd.Execute()

	if err != nil {
		t.Fatalf("expected top alerts to run, got %v stderr %q", err, stderr.String())
	}
	for _, want := range []string{
		"WuKongIM top alerts",
		"id: alert-1",
		"severity: critical",
		"component: channelv2",
		"kind: pressure_high",
		"evidence:",
		"capacity=100",
		"depth=91",
		"threshold.critical=0.95",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected alert detail output to contain %q, got %q", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "HOT PRESSURE") {
		t.Fatalf("alert detail output should not render full overview, got %q", stdout.String())
	}
}

func TestTopAlertFilterRendersOneAlertByComponentKind(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(sampleSnapshot())
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"--server", server.URL, "--alert", "channelv2/pressure_high"})

	err := cmd.Execute()

	if err != nil {
		t.Fatalf("expected top alert detail to run, got %v stderr %q", err, stderr.String())
	}
	for _, want := range []string{"WuKongIM top alerts", "id: alert-1", "message: append worker queue saturated"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected filtered alert output to contain %q, got %q", want, stdout.String())
		}
	}
}

func TestTopAlertFilterReturnsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(sampleSnapshot())
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"--server", server.URL, "--alert", "gateway/session_error"})

	err := cmd.Execute()

	var exit command.Exit
	if !errors.As(err, &exit) {
		t.Fatalf("expected command exit, got %v", err)
	}
	if exit.Code != command.ExitInternal {
		t.Fatalf("exit code = %d, want %d", exit.Code, command.ExitInternal)
	}
	if !strings.Contains(exit.Message, "top alert not found") {
		t.Fatalf("expected not found message, got %q", exit.Message)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout for missing alert, got %q", stdout.String())
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

func TestTopInteractiveMaxRefreshRunsRepeatedSnapshots(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_ = json.NewEncoder(w).Encode(sampleSnapshot())
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"--server", server.URL, "--max-refresh", "2", "--interval", "1ms"})

	err := cmd.Execute()

	if err != nil {
		t.Fatalf("expected bounded interactive top to run, got %v stderr %q", err, stderr.String())
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected 2 snapshot calls, got %d output %q", got, stdout.String())
	}
}

func TestTopInteractiveRejectsNegativeIntervalBeforeNetwork(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"--server", "http://127.0.0.1:1", "--interval", "-1s", "--max-refresh", "1"})

	err := cmd.Execute()

	var exit command.Exit
	if !errors.As(err, &exit) {
		t.Fatalf("expected command exit, got %v", err)
	}
	if exit.Code != command.ExitConfig {
		t.Fatalf("expected config exit code %d, got %d", command.ExitConfig, exit.Code)
	}
	if !strings.Contains(exit.Message, "interval") {
		t.Fatalf("expected interval message, got %q", exit.Message)
	}
}

func TestTopInteractiveWithoutServerReturnsConfigError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	contextDir := t.TempDir()
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr, ContextDir: &contextDir})
	cmd.SetArgs(nil)

	err := cmd.Execute()

	var exit command.Exit
	if !errors.As(err, &exit) {
		t.Fatalf("expected command exit, got %v", err)
	}
	if exit.Code != command.ExitConfig {
		t.Fatalf("expected config exit code %d, got %d", command.ExitConfig, exit.Code)
	}
	if exit.Message != "no server or current context selected" {
		t.Fatalf("expected missing server message, got %q", exit.Message)
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
		Resources: &accessapi.TopResources{
			CPUPercent:     12.5,
			MemoryRSSBytes: 128 << 20,
			MemoryVMSBytes: 256 << 20,
			Goroutines:     99,
			Threads:        8,
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
		Alerts: &accessapi.TopAlerts{
			Counts: accessapi.TopAlertCounts{
				Active:   1,
				Recent:   1,
				Critical: 1,
			},
			Active: []accessapi.TopAlert{{
				ID:          "alert-1",
				Fingerprint: "critical|channelv2|pressure_high|append",
				NodeID:      1,
				NodeName:    "node-1",
				Severity:    "critical",
				Component:   "channelv2",
				Kind:        "pressure_high",
				Message:     "append worker queue saturated",
				Hint:        "scale append workers",
				Evidence: map[string]string{
					"score":              "0.91",
					"depth":              "91",
					"capacity":           "100",
					"threshold.degraded": "0.80",
					"threshold.critical": "0.95",
				},
				FirstSeen: time.Date(2026, 6, 14, 9, 59, 56, 0, time.UTC),
				LastSeen:  time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC),
				Count:     4,
				Active:    true,
			}},
			Recent: []accessapi.TopAlert{{
				ID:          "alert-1",
				Fingerprint: "critical|channelv2|pressure_high|append",
				NodeID:      1,
				NodeName:    "node-1",
				Severity:    "critical",
				Component:   "channelv2",
				Kind:        "pressure_high",
				Message:     "append worker queue saturated",
				Hint:        "scale append workers",
				Evidence: map[string]string{
					"score":              "0.91",
					"depth":              "91",
					"capacity":           "100",
					"threshold.degraded": "0.80",
					"threshold.critical": "0.95",
				},
				FirstSeen: time.Date(2026, 6, 14, 9, 59, 56, 0, time.UTC),
				LastSeen:  time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC),
				Count:     4,
				Active:    true,
			}},
		},
		ChannelRuntime: &accessapi.TopChannelRuntime{
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
