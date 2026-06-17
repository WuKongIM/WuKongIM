package sim

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	contextcmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/context"
)

func TestCommandHelpListsSimulationFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v stderr %q", err, stderr.String())
	}
	for _, want := range []string{
		"--server",
		"--context",
		"--gateway",
		"--bench-token",
		"--users",
		"--groups",
		"--group-members",
		"--rate",
		"--payload-size",
		"--connect-rate",
		"--concurrency",
		"--ack-timeout",
		"--operation-timeout",
		"--run-id",
		"--uid-prefix",
		"--device-prefix",
		"--channel-prefix",
		"--status-listen",
		"--status-interval",
		"--max-runtime",
		"--json",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected help to contain %q, got %q", want, stdout.String())
		}
	}
}

func TestCommandInjectsNormalizedConfig(t *testing.T) {
	orig := execute
	t.Cleanup(func() { execute = orig })
	var captured Config
	execute = func(_ context.Context, _ command.Deps, cfg Config) error {
		captured = cfg
		return nil
	}

	contextDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr, ContextDir: &contextDir})
	cmd.SetArgs([]string{
		"--server", "http://127.0.0.1:5001,http://127.0.0.1:5002",
		"--server", "http://127.0.0.1:5001",
		"--gateway", "127.0.0.1:5100",
		"--bench-token", "bench-secret",
		"--users", "10",
		"--groups", "4",
		"--group-members", "3",
		"--rate", "2.5/s",
		"--payload-size", "1KiB",
		"--connect-rate", "5",
		"--concurrency", "8",
		"--ack-timeout", "3s",
		"--operation-timeout", "4s",
		"--run-id", "fixed-run",
		"--uid-prefix", "u",
		"--device-prefix", "d",
		"--channel-prefix", "g",
		"--status-listen", "127.0.0.1:19092",
		"--status-interval", "6s",
		"--max-runtime", "7s",
		"--json",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v stderr %q", err, stderr.String())
	}
	if captured.ContextDir != contextDir {
		t.Fatalf("ContextDir = %q, want %q", captured.ContextDir, contextDir)
	}
	if !reflect.DeepEqual(captured.Servers, []string{"http://127.0.0.1:5001", "http://127.0.0.1:5002"}) {
		t.Fatalf("Servers = %#v", captured.Servers)
	}
	if !reflect.DeepEqual(captured.Gateways, []string{"127.0.0.1:5100"}) {
		t.Fatalf("Gateways = %#v", captured.Gateways)
	}
	if captured.BenchToken != "bench-secret" || captured.Users != 10 || captured.Groups != 4 || captured.GroupMembers != 3 {
		t.Fatalf("captured topology = %#v", captured)
	}
	if captured.ConnectRate != 5 || captured.Concurrency != 8 || captured.StatusListen != "127.0.0.1:19092" {
		t.Fatalf("captured runtime config = %#v", captured)
	}
	if captured.Rate.PerSecond != 2.5 || captured.RatePerGroup != "2.5/s" || captured.PayloadBytes != 1024 {
		t.Fatalf("captured rate/payload = %#v", captured)
	}
	if captured.AckTimeout != 3*time.Second || captured.OperationTimeout != 4*time.Second || captured.StatusInterval != 6*time.Second || captured.MaxRuntime != 7*time.Second {
		t.Fatalf("captured durations = %#v", captured)
	}
	if captured.RunID != "fixed-run" || captured.UIDPrefix != "u" || captured.DevicePrefix != "d" || captured.ChannelPrefix != "g" || !captured.JSON {
		t.Fatalf("captured identifiers/output = %#v", captured)
	}
}

func TestCommandLoadsNamedContextServers(t *testing.T) {
	orig := execute
	t.Cleanup(func() { execute = orig })
	var captured Config
	execute = func(_ context.Context, _ command.Deps, cfg Config) error {
		captured = cfg
		return nil
	}

	contextDir := t.TempDir()
	store := contextcmd.NewStore(contextDir)
	if err := store.Save(contextcmd.Context{
		Name:    "dev",
		Servers: []string{"http://127.0.0.1:5001", "http://127.0.0.1:5002"},
	}); err != nil {
		t.Fatalf("save context: %v", err)
	}
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr, ContextDir: &contextDir})
	cmd.SetArgs([]string{"--context", "dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v stderr %q", err, stderr.String())
	}
	if !reflect.DeepEqual(captured.Servers, []string{"http://127.0.0.1:5001", "http://127.0.0.1:5002"}) {
		t.Fatalf("Servers = %#v", captured.Servers)
	}
	if captured.ContextName != "dev" {
		t.Fatalf("ContextName = %q, want dev", captured.ContextName)
	}
}

func TestCommandLoadsCurrentContextServers(t *testing.T) {
	orig := execute
	t.Cleanup(func() { execute = orig })
	var captured Config
	execute = func(_ context.Context, _ command.Deps, cfg Config) error {
		captured = cfg
		return nil
	}

	contextDir := t.TempDir()
	store := contextcmd.NewStore(contextDir)
	if err := store.Save(contextcmd.Context{Name: "dev", Servers: []string{"http://127.0.0.1:5001"}}); err != nil {
		t.Fatalf("save context: %v", err)
	}
	if err := store.Select("dev"); err != nil {
		t.Fatalf("select context: %v", err)
	}
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr, ContextDir: &contextDir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v stderr %q", err, stderr.String())
	}
	if !reflect.DeepEqual(captured.Servers, []string{"http://127.0.0.1:5001"}) {
		t.Fatalf("Servers = %#v", captured.Servers)
	}
	if captured.ContextName != "dev" {
		t.Fatalf("ContextName = %q, want dev", captured.ContextName)
	}
}

func TestCommandServerFlagOverridesCurrentContext(t *testing.T) {
	orig := execute
	t.Cleanup(func() { execute = orig })
	var captured Config
	execute = func(_ context.Context, _ command.Deps, cfg Config) error {
		captured = cfg
		return nil
	}

	contextDir := t.TempDir()
	store := contextcmd.NewStore(contextDir)
	if err := store.Save(contextcmd.Context{Name: "dev", Servers: []string{"http://127.0.0.1:5001"}}); err != nil {
		t.Fatalf("save context: %v", err)
	}
	if err := store.Select("dev"); err != nil {
		t.Fatalf("select context: %v", err)
	}
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr, ContextDir: &contextDir})
	cmd.SetArgs([]string{"--server", "http://127.0.0.1:6001"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v stderr %q", err, stderr.String())
	}
	if !reflect.DeepEqual(captured.Servers, []string{"http://127.0.0.1:6001"}) {
		t.Fatalf("Servers = %#v", captured.Servers)
	}
	if captured.ContextName != "" {
		t.Fatalf("ContextName = %q, want empty", captured.ContextName)
	}
}

func TestCommandRejectsInvalidConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"--server", "http://127.0.0.1:5001", "--users", "-1"})

	err := cmd.Execute()
	var exit command.Exit
	if !errors.As(err, &exit) {
		t.Fatalf("expected command.Exit, got %v", err)
	}
	if exit.Code != command.ExitConfig {
		t.Fatalf("exit code = %d, want %d", exit.Code, command.ExitConfig)
	}
}

func TestExecuteConfigRunsRuntimeAndRendersFinalStatus(t *testing.T) {
	orig := runSimulation
	t.Cleanup(func() { runSimulation = orig })
	runSimulation = func(ctx context.Context, cfg Config, status *statusModel) error {
		status.setState(stateRunning)
		status.setTarget(cfg.Servers, cfg.Gateways)
		status.setTopology(1, 1, 1)
		status.addMessagesSent(1)
		return nil
	}

	cfg, err := normalizeConfig(Config{
		Servers:        []string{"http://127.0.0.1:5001"},
		Gateways:       []string{"127.0.0.1:5100"},
		Users:          1,
		Groups:         1,
		GroupMembers:   1,
		RatePerGroup:   "1/s",
		RunID:          "run-1",
		StatusListen:   "127.0.0.1:0",
		StatusInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := executeConfig(context.Background(), command.Deps{Stdout: &stdout, Stderr: &stderr}, cfg); err != nil {
		t.Fatalf("executeConfig() error = %v stderr %q", err, stderr.String())
	}
	for _, want := range []string{"wkcli sim", "run-1", "messages=1"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, stdout.String())
		}
	}
}
