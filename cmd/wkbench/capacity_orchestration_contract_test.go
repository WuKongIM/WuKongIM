package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/capacity"
	"github.com/WuKongIM/WuKongIM/internal/bench/messageevent"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

type fakeCapacityRunner struct {
	result capacity.Result
	err    error
}

func (f fakeCapacityRunner) Run(context.Context) (capacity.Result, error) {
	return f.result, f.err
}

type fakeMessageEventRunner struct {
	result messageevent.Result
	err    error
}

func (f fakeMessageEventRunner) Run(context.Context) (messageevent.Result, error) {
	return f.result, f.err
}

func preserveCapacityCommandSeams(t *testing.T) {
	t.Helper()
	origDiscover := discoverCapacityTarget
	origNewCapacity := newCapacityRunner
	origNewHotChannel := newHotChannelRunner
	origNewMessageEvent := newMessageEventRunner
	t.Cleanup(func() {
		discoverCapacityTarget = origDiscover
		newCapacityRunner = origNewCapacity
		newHotChannelRunner = origNewHotChannel
		newMessageEventRunner = origNewMessageEvent
	})
}

func TestCapacitySendCommandPreservesDiscoveryAndPublishesTerminalEvidence(t *testing.T) {
	preserveCapacityCommandSeams(t)
	reportDir := t.TempDir()
	cfg := capacity.DefaultConfig()
	cfg.APIAddrs = []string{"http://node-1", "http://node-2"}
	cfg.GatewayTCPAddrs = []string{"node-1:5100"}
	cfg.ReportDir = reportDir
	discovered := capacity.DiscoveredTarget{Target: model.Target{Name: "discovered-target"}}
	var discoverCfg capacity.Config
	var runnerCfg capacity.Config
	var runnerTarget capacity.DiscoveredTarget
	discoverCapacityTarget = func(_ context.Context, got capacity.Config) (capacity.DiscoveredTarget, error) {
		discoverCfg = got
		return discovered, nil
	}
	newCapacityRunner = func(got capacity.Config, target capacity.DiscoveredTarget) capacityRunner {
		runnerCfg = got
		runnerTarget = target
		return fakeCapacityRunner{result: capacity.Result{
			Status:       capacity.StatusPassed,
			Profile:      got.Profile,
			MaxStableQPS: got.StartQPS,
			ReportDir:    got.ReportDir,
		}}
	}

	var stderr bytes.Buffer
	code := runCapacitySendConfig(cfg, &stderr)

	if code != 0 {
		t.Fatalf("run capacity send = %d, stderr %q", code, stderr.String())
	}
	if !reflect.DeepEqual(discoverCfg, cfg) || !reflect.DeepEqual(runnerCfg, cfg) {
		t.Fatalf("config changed across discovery: discover=%+v runner=%+v want=%+v", discoverCfg, runnerCfg, cfg)
	}
	if !reflect.DeepEqual(runnerTarget, discovered) {
		t.Fatalf("runner target = %+v, want %+v", runnerTarget, discovered)
	}
	assertFileContains(t, filepath.Join(reportDir, "result.json"), `"status": "passed"`)
	assertFileContains(t, filepath.Join(reportDir, "summary.md"), "max_stable_qps")
	if !strings.Contains(stderr.String(), "status: passed") {
		t.Fatalf("terminal summary = %q", stderr.String())
	}
}

func TestCapacityCommandsDoNotStartWorkloadWhenDiscoveryFails(t *testing.T) {
	preserveCapacityCommandSeams(t)
	discoverCapacityTarget = func(context.Context, capacity.Config) (capacity.DiscoveredTarget, error) {
		return capacity.DiscoveredTarget{}, errors.New("readiness evidence unavailable")
	}
	started := 0
	newCapacityRunner = func(capacity.Config, capacity.DiscoveredTarget) capacityRunner {
		started++
		return fakeCapacityRunner{}
	}
	newHotChannelRunner = func(capacity.HotChannelConfig, capacity.DiscoveredTarget) capacityRunner {
		started++
		return fakeCapacityRunner{}
	}

	var sendErr bytes.Buffer
	if code := runCapacitySendConfig(capacity.DefaultConfig(), &sendErr); code != exitPreflight {
		t.Fatalf("send exit = %d, want %d", code, exitPreflight)
	}
	var hotErr bytes.Buffer
	if code := runCapacityHotChannelConfig(capacity.DefaultHotChannelConfig(), &hotErr); code != exitPreflight {
		t.Fatalf("hot-channel exit = %d, want %d", code, exitPreflight)
	}
	if started != 0 {
		t.Fatalf("started %d workloads after failed discovery", started)
	}
	for _, output := range []string{sendErr.String(), hotErr.String()} {
		if !strings.Contains(output, "capacity preflight failed: readiness evidence unavailable") {
			t.Fatalf("preflight output = %q", output)
		}
	}
}

func TestCapacityHotChannelCommandPersistsResultBeforeReturningRunnerFailure(t *testing.T) {
	preserveCapacityCommandSeams(t)
	reportDir := t.TempDir()
	cfg := capacity.DefaultHotChannelConfig()
	cfg.ReportDir = reportDir
	discoverCapacityTarget = func(context.Context, capacity.Config) (capacity.DiscoveredTarget, error) {
		return capacity.DiscoveredTarget{}, nil
	}
	newHotChannelRunner = func(got capacity.HotChannelConfig, _ capacity.DiscoveredTarget) capacityRunner {
		if got.Senders != cfg.Senders {
			t.Fatalf("senders = %d, want %d", got.Senders, cfg.Senders)
		}
		return fakeCapacityRunner{
			result: capacity.Result{Status: capacity.StatusFailed, Profile: "hot-channel", ReportDir: reportDir},
			err:    errors.New("worker generation stopped"),
		}
	}

	var stderr bytes.Buffer
	code := runCapacityHotChannelConfig(cfg, &stderr)

	if code != exitWorker {
		t.Fatalf("hot-channel exit = %d, want %d", code, exitWorker)
	}
	assertFileContains(t, filepath.Join(reportDir, "result.json"), `"status": "failed"`)
	if !strings.Contains(stderr.String(), "capacity run failed: worker generation stopped") {
		t.Fatalf("runner failure output = %q", stderr.String())
	}
}

func TestCapacityCommandReturnsInternalWhenTerminalEvidenceCannotBePublished(t *testing.T) {
	preserveCapacityCommandSeams(t)
	root := t.TempDir()
	notDirectory := filepath.Join(root, "occupied")
	if err := os.WriteFile(notDirectory, []byte("file"), 0o600); err != nil {
		t.Fatalf("write occupied path: %v", err)
	}
	discoverCapacityTarget = func(context.Context, capacity.Config) (capacity.DiscoveredTarget, error) {
		return capacity.DiscoveredTarget{}, nil
	}
	newCapacityRunner = func(capacity.Config, capacity.DiscoveredTarget) capacityRunner {
		return fakeCapacityRunner{result: capacity.Result{Status: capacity.StatusPassed, ReportDir: notDirectory}}
	}

	var stderr bytes.Buffer
	if code := runCapacitySendConfig(capacity.DefaultConfig(), &stderr); code != exitInternal {
		t.Fatalf("capacity send exit = %d, want %d", code, exitInternal)
	}
	if !strings.Contains(stderr.String(), "capacity report write failed") {
		t.Fatalf("publication failure output = %q", stderr.String())
	}
}

func TestCapacityMessageEventCommandPersistsResultAndMapsTerminalStatus(t *testing.T) {
	preserveCapacityCommandSeams(t)

	for _, tc := range []struct {
		name     string
		status   string
		runErr   error
		wantCode int
		wantText string
	}{
		{name: "passed", status: messageevent.StatusPassed, wantCode: 0},
		{name: "failed_gate", status: messageevent.StatusFailed, wantCode: exitWorker},
		{name: "runner_error", status: messageevent.StatusFailed, runErr: errors.New("metrics cut failed"), wantCode: exitWorker, wantText: "message-event run failed: metrics cut failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reportDir := t.TempDir()
			newMessageEventRunner = func(cfg messageevent.Config) messageEventRunner {
				return fakeMessageEventRunner{result: messageevent.Result{
					Status:    tc.status,
					RunID:     cfg.RunID,
					Shape:     cfg.Shape(),
					ReportDir: reportDir,
				}, err: tc.runErr}
			}
			cfg := messageevent.DefaultConfig()
			cfg.RunID = "event-contract"
			var stderr bytes.Buffer

			if code := runCapacityMessageEventConfig(cfg, &stderr); code != tc.wantCode {
				t.Fatalf("message-event exit = %d, want %d, stderr %q", code, tc.wantCode, stderr.String())
			}
			assertFileContains(t, filepath.Join(reportDir, "message_event_report.json"), `"run_id": "event-contract"`)
			if tc.wantText != "" && !strings.Contains(stderr.String(), tc.wantText) {
				t.Fatalf("runner failure output = %q", stderr.String())
			}
		})
	}
}

func TestActivateChannelsDiscoveryConfigCopiesAuthorityAndEndpoints(t *testing.T) {
	cfg := capacity.DefaultActivateChannelsConfig()
	cfg.APIAddrs = []string{"http://node-1", "http://node-2"}
	cfg.GatewayTCPAddrs = []string{"node-1:5100"}
	cfg.BenchToken = "bench-token"
	cfg.GroupMembers = 100000
	cfg.ReportDir = "/reports/activation"

	got := activateChannelsDiscoveryConfig(cfg)

	if !reflect.DeepEqual(got.APIAddrs, cfg.APIAddrs) || !reflect.DeepEqual(got.GatewayTCPAddrs, cfg.GatewayTCPAddrs) {
		t.Fatalf("discovery endpoints = %+v/%+v", got.APIAddrs, got.GatewayTCPAddrs)
	}
	if got.BenchToken != cfg.BenchToken || got.GroupMembers != cfg.GroupMembers || got.ReportDir != cfg.ReportDir {
		t.Fatalf("discovery authority/shape = %+v", got)
	}
	got.APIAddrs[0] = "mutated"
	got.GatewayTCPAddrs[0] = "mutated"
	if cfg.APIAddrs[0] != "http://node-1" || cfg.GatewayTCPAddrs[0] != "node-1:5100" {
		t.Fatalf("discovery config aliases caller endpoint slices: %+v", cfg)
	}
}

func assertFileContains(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("%s = %q, want substring %q", path, data, want)
	}
}
