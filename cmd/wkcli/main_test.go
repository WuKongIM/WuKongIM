package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	"github.com/spf13/cobra"
)

func TestRootCommandHelpListsPlannedSubcommands(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := runWithIO([]string{"--help"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("expected help exit code %d, got %d stderr %q", exitOK, code, stderr.String())
	}
	for _, want := range []string{"wkcli", "context", "top", "bench"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected help to contain %q, got %q", want, stdout.String())
		}
	}
}

func TestRootCommandRequiresSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := runWithIO(nil, &stdout, &stderr)

	if code != exitConfig {
		t.Fatalf("expected config exit code %d, got %d", exitConfig, code)
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("expected root help, got stdout %q stderr %q", stdout.String(), stderr.String())
	}
}

func TestTopCommandHelpListsOnceFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := runWithIO([]string{"top", "--help"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("expected help exit code %d, got %d stderr %q", exitOK, code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "--once") {
		t.Fatalf("expected top help to list --once, got %q", stdout.String())
	}
}

func TestBenchCommandRequiresSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := runWithIO([]string{"bench"}, &stdout, &stderr)

	if code != exitConfig {
		t.Fatalf("expected config exit code %d, got %d stdout %q stderr %q", exitConfig, code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "send") {
		t.Fatalf("expected bench help to list send command, got stdout %q stderr %q", stdout.String(), stderr.String())
	}
}

func TestRootCommandAcceptsAdditionalSubcommandFactories(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := command.Deps{Stdout: &stdout, Stderr: &stderr}
	cmd := newRootCommand(deps, []command.Factory{
		func(deps command.Deps) *cobra.Command {
			return &cobra.Command{
				Use:   "doctor",
				Short: "Inspect a WuKongIM cluster",
				RunE: func(cmd *cobra.Command, args []string) error {
					fmt.Fprintln(deps.Stdout, "doctor ok")
					return nil
				},
			}
		},
	})
	cmd.SetArgs([]string{"doctor"})

	err := cmd.Execute()

	if err != nil {
		t.Fatalf("expected command to run, got %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "doctor ok" {
		t.Fatalf("expected injected command output, got stdout %q stderr %q", stdout.String(), stderr.String())
	}
}

func TestContextCommandAddSelectShowListCurrentAndRemove(t *testing.T) {
	contextDir := t.TempDir()

	stdout, stderr, code := runCLI(
		"--context-dir", contextDir,
		"context", "add", "dev",
		"--server", "http://127.0.0.1:5001",
		"--server", "http://127.0.0.1:5002",
		"--description", "local three-node cluster",
		"--select",
	)
	if code != exitOK {
		t.Fatalf("expected add exit code %d, got %d stdout %q stderr %q", exitOK, code, stdout, stderr)
	}
	for _, want := range []string{"saved context dev", "selected context dev"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected add output to contain %q, got %q", want, stdout)
		}
	}

	stdout, stderr, code = runCLI("--context-dir", contextDir, "context", "show", "dev")
	if code != exitOK {
		t.Fatalf("expected show exit code %d, got %d stdout %q stderr %q", exitOK, code, stdout, stderr)
	}
	for _, want := range []string{"name: dev", "description: local three-node cluster", "http://127.0.0.1:5001", "http://127.0.0.1:5002"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected show output to contain %q, got %q", want, stdout)
		}
	}

	stdout, stderr, code = runCLI("--context-dir", contextDir, "context", "ls")
	if code != exitOK {
		t.Fatalf("expected list exit code %d, got %d stdout %q stderr %q", exitOK, code, stdout, stderr)
	}
	if !strings.Contains(stdout, "* dev") {
		t.Fatalf("expected current marker in list output, got %q", stdout)
	}

	stdout, stderr, code = runCLI("--context-dir", contextDir, "context", "current")
	if code != exitOK {
		t.Fatalf("expected current exit code %d, got %d stdout %q stderr %q", exitOK, code, stdout, stderr)
	}
	if strings.TrimSpace(stdout) != "dev" {
		t.Fatalf("expected current context dev, got stdout %q stderr %q", stdout, stderr)
	}

	stdout, stderr, code = runCLI("--context-dir", contextDir, "context", "rm", "dev")
	if code != exitOK {
		t.Fatalf("expected remove exit code %d, got %d stdout %q stderr %q", exitOK, code, stdout, stderr)
	}
	for _, want := range []string{"removed context dev", "cleared current context"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected remove output to contain %q, got %q", want, stdout)
		}
	}
}

func TestContextCommandAddAcceptsCommaSeparatedServers(t *testing.T) {
	contextDir := t.TempDir()

	stdout, stderr, code := runCLI(
		"--context-dir", contextDir,
		"context", "add", "dev",
		"--server", "http://127.0.0.1:5001, http://127.0.0.1:5002",
	)

	if code != exitOK {
		t.Fatalf("expected add exit code %d, got %d stdout %q stderr %q", exitOK, code, stdout, stderr)
	}
	stdout, stderr, code = runCLI("--context-dir", contextDir, "context", "show", "dev")
	if code != exitOK {
		t.Fatalf("expected show exit code %d, got %d stdout %q stderr %q", exitOK, code, stdout, stderr)
	}
	for _, want := range []string{"http://127.0.0.1:5001", "http://127.0.0.1:5002"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected show output to contain %q, got %q", want, stdout)
		}
	}
}

func TestContextCommandAddRequiresServer(t *testing.T) {
	stdout, stderr, code := runCLI(
		"--context-dir", t.TempDir(),
		"context", "add", "dev",
	)

	if code != exitConfig {
		t.Fatalf("expected config exit code %d, got %d stdout %q stderr %q", exitConfig, code, stdout, stderr)
	}
	if !strings.Contains(stderr, "--server is required") {
		t.Fatalf("expected server error, got stdout %q stderr %q", stdout, stderr)
	}
}

func runCLI(args ...string) (string, string, int) {
	var stdout, stderr bytes.Buffer
	code := runWithIO(args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}
