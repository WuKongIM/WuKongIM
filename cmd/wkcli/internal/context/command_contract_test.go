package contextcmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
)

func TestContextCommandsManageSelectionLifecycle(t *testing.T) {
	dir := t.TempDir()

	stdout, err := executeContextCommand(dir, "ls")
	if err != nil || stdout != "no contexts\n" {
		t.Fatalf("empty list = %q, %v", stdout, err)
	}
	stdout, err = executeContextCommand(
		dir, "add", "alpha",
		"--server", "http://node-1:5001, https://node-2:5001",
		"--description", "  primary cluster  ", "--select",
	)
	if err != nil || stdout != "saved context alpha\nselected context alpha\n" {
		t.Fatalf("add and select = %q, %v", stdout, err)
	}
	stdout, err = executeContextCommand(dir, "add", "beta", "--server", "http://node-3:5001")
	if err != nil || stdout != "saved context beta\n" {
		t.Fatalf("add beta = %q, %v", stdout, err)
	}

	stdout, err = executeContextCommand(dir, "ls")
	if err != nil {
		t.Fatalf("list contexts: %v", err)
	}
	if !strings.Contains(stdout, "* alpha\thttp://node-1:5001,https://node-2:5001\tprimary cluster") ||
		!strings.Contains(stdout, "  beta\thttp://node-3:5001\t") ||
		strings.Index(stdout, "alpha") > strings.Index(stdout, "beta") {
		t.Fatalf("unexpected context list:\n%s", stdout)
	}

	stdout, err = executeContextCommand(dir, "current")
	if err != nil || stdout != "alpha\n" {
		t.Fatalf("current = %q, %v", stdout, err)
	}
	stdout, err = executeContextCommand(dir, "show")
	if err != nil || !strings.Contains(stdout, "name: alpha\n") ||
		!strings.Contains(stdout, "description: primary cluster\n") ||
		!strings.Contains(stdout, "  - https://node-2:5001\n") {
		t.Fatalf("implicit show = %q, %v", stdout, err)
	}
	stdout, err = executeContextCommand(dir, "show", "beta")
	if err != nil || strings.Contains(stdout, "description:") || !strings.Contains(stdout, "name: beta\n") {
		t.Fatalf("explicit show = %q, %v", stdout, err)
	}

	stdout, err = executeContextCommand(dir, "select", "beta")
	if err != nil || stdout != "selected context beta\n" {
		t.Fatalf("select beta = %q, %v", stdout, err)
	}
	stdout, err = executeContextCommand(dir, "rm", "alpha")
	if err != nil || stdout != "removed context alpha\n" {
		t.Fatalf("remove non-current = %q, %v", stdout, err)
	}
	stdout, err = executeContextCommand(dir, "remove", "beta")
	if err != nil || stdout != "removed context beta\ncleared current context\n" {
		t.Fatalf("remove current = %q, %v", stdout, err)
	}

	for _, args := range [][]string{{"current"}, {"show"}, {"select", "missing"}, {"rm", "missing"}} {
		_, err := executeContextCommand(dir, args...)
		var exit command.Exit
		if !errors.As(err, &exit) || exit.Code != command.ExitConfig || exit.Message == "" {
			t.Fatalf("%v error = %#v", args, err)
		}
	}
}

func TestContextCommandRootShowsHelpAndReturnsConfigExit(t *testing.T) {
	dir := t.TempDir()
	stdout, err := executeContextCommand(dir)
	var exit command.Exit
	if !errors.As(err, &exit) || exit.Code != command.ExitConfig {
		t.Fatalf("root command error = %#v", err)
	}
	if !strings.Contains(stdout, "Manage named WuKongIM server contexts") || !strings.Contains(stdout, "Available Commands") {
		t.Fatalf("root help = %q", stdout)
	}
}

func TestContextCommandRejectsInvalidConfiguration(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"add", "../unsafe", "--server", "http://node-1:5001"},
		{"add", "missing-server"},
		{"add", "bad-scheme", "--server", "ftp://node-1"},
		{"add", "duplicate", "--server", "http://node-1,http://node-1"},
	} {
		_, err := executeContextCommand(dir, args...)
		var exit command.Exit
		if !errors.As(err, &exit) || exit.Code != command.ExitConfig {
			t.Fatalf("%v error = %#v", args, err)
		}
	}
}

func TestStoreFromDepsHonorsExplicitDirectory(t *testing.T) {
	dir := t.TempDir()
	if got := storeFromDeps(command.Deps{ContextDir: &dir}).dir; got != dir {
		t.Fatalf("explicit store dir = %q, want %q", got, dir)
	}
	empty := "  "
	if got := storeFromDeps(command.Deps{ContextDir: &empty}).dir; strings.TrimSpace(got) == "" {
		t.Fatal("empty context dir did not use the user-level default")
	}
	if got := storeFromDeps(command.Deps{}).dir; strings.TrimSpace(got) == "" {
		t.Fatal("nil context dir did not use the user-level default")
	}
}

func executeContextCommand(dir string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{
		Stdout:     &stdout,
		Stderr:     &stderr,
		ContextDir: &dir,
	})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), err
}
