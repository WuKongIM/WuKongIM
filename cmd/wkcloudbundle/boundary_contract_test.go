package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadBundleSpecRejectsUnboundedOrAmbiguousJSON(t *testing.T) {
	t.Parallel()

	valid := []byte(`{"run_id":"r","source_sha":"0123456789012345678901234567890123456789","scenario_path":"s.yaml","scenario_digest":"sha256:s","duration":"24h","private_ipv4":{"node-1":"1"},"simulator_source_ipv4":["4"],"public_observation":true}`)
	tests := []struct {
		name string
		body []byte
	}{
		{name: "unknown field", body: append(append([]byte(nil), valid[:len(valid)-1]...), []byte(`,"secret":"no"}`)...)},
		{name: "trailing value", body: append(append([]byte(nil), valid...), []byte(` {}`)...)},
		{name: "invalid duration", body: bytes.Replace(valid, []byte(`"24h"`), []byte(`"forever"`), 1)},
		{name: "oversized trailing whitespace", body: append(append([]byte(nil), valid...), bytes.Repeat([]byte(" "), maxBundleSpecBytes)...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "spec.json")
			if err := os.WriteFile(path, test.body, 0o600); err != nil {
				t.Fatalf("WriteFile(): %v", err)
			}
			if _, err := readBundleSpec(path); err == nil {
				t.Fatal("readBundleSpec() error = nil, want strict rejection")
			}
		})
	}
}

func TestExecuteReportsCommandFailuresWithoutSideEffects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "unknown command", args: []string{"unknown"}},
		{name: "missing verify bundle", args: []string{"verify", "--root", filepath.Join(t.TempDir(), "missing")}},
		{name: "missing offline bundle", args: []string{"verify-offline", "--root", filepath.Join(t.TempDir(), "missing")}},
		{name: "missing render spec", args: []string{"render", "--root", t.TempDir(), "--spec", filepath.Join(t.TempDir(), "missing.json")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := execute(test.args, &stdout, &stderr); code != 1 {
				t.Fatalf("execute(%v) = %d, want 1", test.args, code)
			}
			if strings.TrimSpace(stderr.String()) == "" {
				t.Fatalf("execute(%v) did not report its failure", test.args)
			}
		})
	}
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"--help"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "wkcloudbundle") {
		t.Fatalf("execute(--help) = code %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}
