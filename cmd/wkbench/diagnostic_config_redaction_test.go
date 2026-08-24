package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportRedactConfigWritesPrivateStructuredOutput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "wukongim.toml")
	output := filepath.Join(dir, "effective-wukongim.toml")
	const canary = "report-redact-config-canary"
	if err := os.WriteFile(input, []byte("[bench]\napi_token = \""+canary+"\"\napi_enable = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	if code := executeRoot([]string{"report", "redact-config", "--input", input, "--output", output}, &stderr); code != 0 {
		t.Fatalf("report redact-config exit = %d: %s", code, stderr.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), canary) || !strings.Contains(string(data), "******") {
		t.Fatalf("redacted output = %s", data)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("redacted output mode = %04o, want 0600", got)
	}
}

func TestReportRedactConfigDoesNotPublishPartialOutputOnFailure(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "wukongim.toml")
	output := filepath.Join(dir, "effective-wukongim.toml")
	if err := os.WriteFile(input, []byte("[unknown]\nsecret = \"canary\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	if code := executeRoot([]string{"report", "redact-config", "--input", input, "--output", output}, &stderr); code == 0 {
		t.Fatalf("unknown config unexpectedly succeeded: %s", stderr.String())
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("failed redaction published output: %v", err)
	}
}

func TestReportRedactConfigRejectsSymlinkInputAndExistingOutput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "wukongim.toml")
	inputLink := filepath.Join(dir, "linked.toml")
	output := filepath.Join(dir, "effective-wukongim.toml")
	if err := os.WriteFile(input, []byte("[bench]\napi_token = \"canary\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(input, inputLink); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	if code := executeRoot([]string{"report", "redact-config", "--input", inputLink, "--output", output}, &stderr); code == 0 {
		t.Fatalf("symlink input unexpectedly succeeded: %s", stderr.String())
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("symlink input published output: %v", err)
	}

	const sentinel = "preserve-existing-output\n"
	if err := os.WriteFile(output, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := executeRoot([]string{"report", "redact-config", "--input", input, "--output", output}, &stderr); code == 0 {
		t.Fatalf("existing output unexpectedly replaced: %s", stderr.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != sentinel {
		t.Fatalf("existing output changed to %q", data)
	}
}
