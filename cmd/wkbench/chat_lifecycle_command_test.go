package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
)

func TestRootHelpListsSoakAndPreservesExistingCommands(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithStderr([]string{"--help"}, &stderr)
	if code != 0 {
		t.Fatalf("root help code = %d, stderr = %q", code, stderr.String())
	}
	for _, command := range []string{"soak", "run", "worker", "validate", "doctor", "dev-sim", "capacity", "metrics", "report"} {
		if !strings.Contains(stderr.String(), command) {
			t.Fatalf("root help does not contain %q: %q", command, stderr.String())
		}
	}
}

func TestSoakChatLifecycleHelp(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithStderr([]string{"soak", "chat-lifecycle", "--help"}, &stderr)
	if code != 0 {
		t.Fatalf("soak chat-lifecycle help code = %d, stderr = %q", code, stderr.String())
	}
	for _, want := range []string{"chat-lifecycle", "--config", "--duration", "--output-dir"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("soak chat-lifecycle help does not contain %q: %q", want, stderr.String())
		}
	}
}

func TestSoakChatLifecycleAppliesDirectRehearsalDuration(t *testing.T) {
	cli := chatLifecycleCLIConfig{
		configPath: filepath.Join(findRepoRoot(t), "configs", "wkbench", "chat-lifecycle", "rehearsal.yaml"),
		outputDir:  t.TempDir(),
		duration:   72*time.Hour + 15*time.Minute,
	}
	if err := loadSoakChatLifecycleConfig(&cli); err != nil {
		t.Fatal(err)
	}
	if cli.config.RunDuration != cli.duration {
		t.Fatalf("run duration = %v, want %v", cli.config.RunDuration, cli.duration)
	}
}

func TestSoakChatLifecycleRequiresExplicitConfigAndOutputDirectory(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "config", args: []string{"soak", "chat-lifecycle"}, want: "--config is required"},
		{name: "output directory", args: []string{"soak", "chat-lifecycle", "--config", "unused.yaml"}, want: "--output-dir is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := runWithStderr(test.args, &stderr)
			if code != exitConfig || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("code/stderr = %d/%q, want %d containing %q", code, stderr.String(), exitConfig, test.want)
			}
		})
	}
}

func TestCapacityHelpAddsChatLifecycleAndPreservesExistingSubcommands(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithStderr([]string{"capacity", "--help"}, &stderr)
	if code != 0 {
		t.Fatalf("capacity help code = %d, stderr = %q", code, stderr.String())
	}
	for _, command := range []string{"chat-lifecycle", "send", "hot-channel", "activate-channels", "message-event"} {
		if !strings.Contains(stderr.String(), command) {
			t.Fatalf("capacity help does not contain %q: %q", command, stderr.String())
		}
	}
}

func TestCapacityChatLifecycleHelp(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithStderr([]string{"capacity", "chat-lifecycle", "--help"}, &stderr)
	if code != 0 {
		t.Fatalf("capacity chat-lifecycle help code = %d, stderr = %q", code, stderr.String())
	}
	for _, want := range []string{"chat-lifecycle", "--config", "--checkpoint", "--output-dir"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("capacity chat-lifecycle help does not contain %q: %q", want, stderr.String())
		}
	}
}

func TestCapacityChatLifecycleRequiresExplicitPaths(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "config", args: []string{"capacity", "chat-lifecycle"}, want: "--config is required"},
		{name: "checkpoint", args: []string{"capacity", "chat-lifecycle", "--config", "unused.yaml"}, want: "--checkpoint is required"},
		{name: "output directory", args: []string{"capacity", "chat-lifecycle", "--config", "unused.yaml", "--checkpoint", "unused.json"}, want: "--output-dir is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := runWithStderr(test.args, &stderr)
			if code != exitConfig || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("code/stderr = %d/%q, want %d containing %q", code, stderr.String(), exitConfig, test.want)
			}
		})
	}
}

func TestCapacityChatLifecycleRejectsUnreadableCheckpointDuringParsing(t *testing.T) {
	cfg := chatlifecycle.FormalConfig()
	cfg.Mode = chatlifecycle.ModeCapacity
	cfg.Capacity.AgedCheckpoint = chatlifecycle.AgedCheckpoint{
		Reference: "provided-by-checkpoint-flag",
		Completed: true,
		Passed:    true,
		Duration:  72 * time.Hour,
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := writeWkbenchTempFile(t, string(body))
	missingCheckpoint := filepath.Join(t.TempDir(), "missing-72h.json")
	var stderr bytes.Buffer

	_, code := parseCapacityChatLifecycleConfig([]string{
		"--config", configPath,
		"--checkpoint", missingCheckpoint,
		"--output-dir", t.TempDir(),
	}, &stderr)

	if code != exitConfig || !strings.Contains(stderr.String(), "checkpoint") {
		t.Fatalf("parse code/stderr = %d/%q, want %d checkpoint error", code, stderr.String(), exitConfig)
	}
}

func TestChatLifecycleCommandsRejectUnknownFlags(t *testing.T) {
	for _, args := range [][]string{
		{"soak", "chat-lifecycle", "--config", "unused.yaml", "--output-dir", "unused", "--unknown-lifecycle-flag"},
		{"capacity", "chat-lifecycle", "--config", "unused.yaml", "--checkpoint", "unused.json", "--output-dir", "unused", "--unknown-lifecycle-flag"},
	} {
		var stderr bytes.Buffer
		code := runWithStderr(args, &stderr)
		if code != exitConfig || !strings.Contains(stderr.String(), "unknown flag") {
			t.Fatalf("args %v code/stderr = %d/%q, want unknown-flag config failure", args, code, stderr.String())
		}
	}
}

func TestFormalAndCapacityUseMonotonicWorkerGenerationsWithoutRestartingWorkerServers(t *testing.T) {
	if got := chatLifecycleGeneration(chatlifecycle.ModeSoak); got != 1 {
		t.Fatalf("formal generation = %d, want 1", got)
	}
	if got := chatLifecycleGeneration(chatlifecycle.ModeCapacity); got != 2 {
		t.Fatalf("capacity generation = %d, want 2", got)
	}
}

func TestWorkerChatLifecycleHelpPreservesDedicatedModeFlags(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithStderr([]string{"worker", "--mode", "chat-lifecycle", "--help"}, &stderr)
	if code != 0 {
		t.Fatalf("worker chat-lifecycle help code = %d, stderr = %q", code, stderr.String())
	}
	for _, want := range []string{"--mode", "chat-lifecycle", "--listen", "--control-token"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("worker chat-lifecycle help does not contain %q: %q", want, stderr.String())
		}
	}
}

func TestValidateChatLifecycleCommandLoadsStrictFormalSoakConfig(t *testing.T) {
	formal := filepath.Join("..", "..", "configs", "wkbench", "chat-lifecycle", "formal.yaml")
	var stderr bytes.Buffer
	if code := runWithStderr([]string{"validate", "chat-lifecycle", "--config", formal}, &stderr); code != 0 {
		t.Fatalf("validate chat-lifecycle code/stderr = %d/%q", code, stderr.String())
	}

	stderr.Reset()
	invalid := writeWkbenchTempFile(t, "unknown: true\n")
	if code := runWithStderr([]string{"validate", "chat-lifecycle", "--config", invalid}, &stderr); code != exitConfig {
		t.Fatalf("invalid validate chat-lifecycle code/stderr = %d/%q", code, stderr.String())
	}
}
