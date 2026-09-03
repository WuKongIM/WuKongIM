package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/app"
)

func TestVersionCommandTextAndJSON(t *testing.T) {
	oldVersion, oldCommit, oldSource := buildVersion, buildCommit, buildSource
	buildVersion, buildCommit, buildSource = "3.2.1-rc.1", "abc123", "release"
	t.Cleanup(func() {
		buildVersion, buildCommit, buildSource = oldVersion, oldCommit, oldSource
	})

	var text bytes.Buffer
	if err := runVersionCommand(nil, &text); err != nil {
		t.Fatalf("runVersionCommand(text) error = %v", err)
	}
	if got := text.String(); got != "wukongim version=3.2.1-rc.1 commit=abc123 source=release\n" {
		t.Fatalf("text output = %q", got)
	}

	var output bytes.Buffer
	if err := runVersionCommand([]string{"--output", "json"}, &output); err != nil {
		t.Fatalf("runVersionCommand(json) error = %v", err)
	}
	var got buildInfo
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v: %s", err, output.String())
	}
	if got != (buildInfo{Version: "3.2.1-rc.1", Commit: "abc123", BuildSource: "release"}) {
		t.Fatalf("JSON output = %#v", got)
	}
}

func TestVersionCommandRejectsUnknownOutput(t *testing.T) {
	err := runVersionCommand([]string{"--output", "yaml"}, &bytes.Buffer{})
	if commandExitCode(err) != exitUsage {
		t.Fatalf("commandExitCode() = %d, want %d: %v", commandExitCode(err), exitUsage, err)
	}
}

func TestExecuteInitAliasCreatesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wukongim.toml")
	var stdout bytes.Buffer
	createdApp := false
	err := execute(context.Background(), []string{
		"init",
		"--config", path,
		"--admin-password-stdin",
	}, commandIO{
		stdin:  strings.NewReader("operator-secret-password\n"),
		stdout: &stdout,
	}, func(app.Config) (runtimeApp, error) {
		createdApp = true
		return &fakeRuntimeApp{}, nil
	})
	if err != nil {
		t.Fatalf("execute(init) error = %v", err)
	}
	if createdApp {
		t.Fatal("execute(init) created the runtime app")
	}
	if !strings.Contains(stdout.String(), "configuration created: "+path) {
		t.Fatalf("stdout = %q", stdout.String())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(body), "operator-secret-password") {
		t.Fatal("generated config does not contain supplied manager password")
	}
}

func TestInitAliasDefaultsToPackageConfigPath(t *testing.T) {
	if defaultPackageConfigPath != "/etc/wukongim/wukongim.toml" {
		t.Fatalf("defaultPackageConfigPath = %q", defaultPackageConfigPath)
	}
}

func TestConfigInitNonInteractiveRequiresPasswordStdin(t *testing.T) {
	err := runConfigInitCommand([]string{"--config", filepath.Join(t.TempDir(), "wukongim.toml")}, commandIO{
		stdin:  strings.NewReader(""),
		stdout: &bytes.Buffer{},
	})
	if commandExitCode(err) != exitUsage || !strings.Contains(err.Error(), "--admin-password-stdin") {
		t.Fatalf("runConfigInitCommand() error = %v code=%d", err, commandExitCode(err))
	}
}

func TestConfigInitDoesNotTreatDevNullAsInteractive(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(%s) error = %v", os.DevNull, err)
	}
	t.Cleanup(func() { _ = devNull.Close() })
	if isTerminal(devNull) {
		t.Fatalf("isTerminal(%s) = true", os.DevNull)
	}

	path := filepath.Join(t.TempDir(), "wukongim.toml")
	err = runConfigInitCommand([]string{"--config", path}, commandIO{
		stdin:          strings.NewReader(""),
		stdout:         devNull,
		stdoutTerminal: isTerminal(devNull),
	})
	if commandExitCode(err) != exitUsage || !strings.Contains(err.Error(), "--admin-password-stdin") {
		t.Fatalf("runConfigInitCommand() error = %v code=%d", err, commandExitCode(err))
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("config was created despite non-interactive output: %v", statErr)
	}
}

func TestConfigInitReadsPasswordFromStdinWithoutEcho(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wukongim.toml")
	var stdout bytes.Buffer
	err := runConfigInitCommand([]string{
		"--config", path,
		"--admin-password-stdin",
	}, commandIO{
		stdin:  strings.NewReader("operator-secret-password\n"),
		stdout: &stdout,
	})
	if err != nil {
		t.Fatalf("runConfigInitCommand() error = %v", err)
	}
	if strings.Contains(stdout.String(), "operator-secret-password") {
		t.Fatalf("stdout disclosed stdin password: %q", stdout.String())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(body), "operator-secret-password") {
		t.Fatal("generated config does not contain supplied manager password")
	}
}

func TestConfigInitRejectsEmptyPasswordFromStdin(t *testing.T) {
	var stdout bytes.Buffer
	err := runConfigInitCommand([]string{
		"--config", filepath.Join(t.TempDir(), "wukongim.toml"),
		"--admin-password-stdin",
	}, commandIO{
		stdin:  strings.NewReader("\n"),
		stdout: &stdout,
	})
	if commandExitCode(err) != exitUsage {
		t.Fatalf("commandExitCode() = %d, want %d: %v", commandExitCode(err), exitUsage, err)
	}
	if !strings.Contains(err.Error(), "password from stdin is empty") {
		t.Fatalf("error = %v", err)
	}
}

func TestConfigInitInteractivePrintsGeneratedPasswordOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wukongim.toml")
	var stdout bytes.Buffer
	err := runConfigInitCommand([]string{"--config", path}, commandIO{
		stdin:          strings.NewReader(""),
		stdout:         &stdout,
		stdoutTerminal: true,
	})
	if err != nil {
		t.Fatalf("runConfigInitCommand() error = %v", err)
	}
	if count := strings.Count(stdout.String(), "manager password:"); count != 1 {
		t.Fatalf("manager password lines = %d: %q", count, stdout.String())
	}
}

func TestConfigValidateIsReadOnly(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "missing-data")
	logDir := filepath.Join(dir, "missing-logs")
	path := filepath.Join(dir, "wukongim.toml")
	writeConf(t, path,
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+dataDir,
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		"WK_LOG_DIR="+logDir,
		"WK_PLUGIN_ENABLE=false",
	)
	var stdout bytes.Buffer
	if err := runConfigValidateCommand([]string{"--config", path}, &stdout); err != nil {
		t.Fatalf("runConfigValidateCommand() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "configuration valid:") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	for _, candidate := range []string{dataDir, logDir} {
		if _, err := os.Stat(candidate); !os.IsNotExist(err) {
			t.Fatalf("validate created %s: %v", candidate, err)
		}
	}
}

func TestConfigValidateReturnsConfigExitCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wukongim.toml")
	if err := os.WriteFile(path, []byte("[node]\nid = 1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	err := runConfigValidateCommand([]string{"--config", path}, &bytes.Buffer{})
	if commandExitCode(err) != exitConfig {
		t.Fatalf("commandExitCode() = %d, want %d: %v", commandExitCode(err), exitConfig, err)
	}
}

func TestExecuteRejectsUnknownCommandWithUsageExit(t *testing.T) {
	err := execute(context.Background(), []string{"unknown"}, commandIO{stdout: &bytes.Buffer{}}, func(app.Config) (runtimeApp, error) {
		return &fakeRuntimeApp{}, nil
	})
	if commandExitCode(err) != exitUsage {
		t.Fatalf("commandExitCode() = %d, want %d: %v", commandExitCode(err), exitUsage, err)
	}
	if !strings.Contains(err.Error(), "init") {
		t.Fatalf("error = %q, want init in expected commands", err)
	}
}
