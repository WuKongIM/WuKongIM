package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalThresholdPprofScriptStaticContract(t *testing.T) {
	root := repoRoot(t)
	scriptPath := filepath.Join(root, "scripts", "capture-wukongim-local-threshold-pprof.sh")
	script := readFile(t, scriptPath)

	for _, want := range []string{
		"#!/usr/bin/env bash", "set -euo pipefail", "umask 077",
		"set +x", "--out-dir", "--phase-state-file", "--trigger-kind", "--trigger-observed-phase",
		"--previous-utc", "--current-utc",
		"--node", "--cpu-seconds", "wukongim.local_threshold_pprof/v1",
		"WK_BENCH_API_TOKEN is required", "unset WK_BENCH_API_TOKEN",
		`--header @<(write_authorization_header)`,
		"sendack_p99", "actual_offered_ratio", "terminal_product_failure", "normalize_rfc3339_nano",
		`--connect-timeout "$CONNECT_TIMEOUT_SECONDS"`, `--max-time "$max_time"`, "CPU_MAX_TIME_SECONDS",
		"/debug/pprof/profile?seconds=", "/debug/pprof/heap", "/debug/pprof/goroutine?debug=2",
		`mkdir "$CLAIM_DIR"`, `capture_start_missed_measurement`,
		`[[ "$END_PHASE" != "measurement" ]]`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("threshold pprof script missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(script), "docker") || strings.Contains(strings.ToLower(script), "aliyun") {
		t.Fatal("local threshold pprof capture must not invoke container or cloud operations")
	}
	for _, forbidden := range []string{`--api-token`, `-H "Authorization: Bearer`, `--header "Authorization: Bearer`} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("threshold pprof script must not expose the API token through %q", forbidden)
		}
	}
	if output, err := exec.Command("bash", "-n", scriptPath).CombinedOutput(); err != nil {
		t.Fatalf("bash syntax failed: %v\n%s", err, output)
	}
}

func TestLocalThresholdPprofScriptRejectsUnsafeParametersBeforeCreatingArtifacts(t *testing.T) {
	root := repoRoot(t)
	scriptPath := filepath.Join(root, "scripts", "capture-wukongim-local-threshold-pprof.sh")
	phasePath := filepath.Join(t.TempDir(), "phase")
	if err := os.WriteFile(phasePath, []byte("measurement\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	validArgs := []string{
		"--out-dir", filepath.Join(t.TempDir(), "capture"),
		"--phase-state-file", phasePath,
		"--trigger-kind", "actual_offered_ratio",
		"--trigger-observed-phase", "measurement",
		"--previous-utc", "2026-08-13T00:00:00Z",
		"--current-utc", "2026-08-13T00:00:01Z",
		"--node", "http://127.0.0.1:5011",
		"--node", "http://127.0.0.1:5012",
		"--node", "http://127.0.0.1:5013",
		"--cpu-seconds", "10",
	}
	tests := []struct {
		name    string
		mutate  func([]string) []string
		message string
	}{
		{
			name: "two nodes",
			mutate: func(args []string) []string {
				return removeNthArgPair(args, "--node", 2)
			},
			message: "exactly one or three --node values are required",
		},
		{
			name: "four nodes",
			mutate: func(args []string) []string {
				return append(args, "--node", "http://127.0.0.1:5014")
			},
			message: "exactly one or three --node values are required",
		},
		{
			name: "unsupported trigger observation phase",
			mutate: func(args []string) []string {
				return replaceArgValue(args, "--trigger-observed-phase", "drain")
			},
			message: "--trigger-observed-phase must be measurement",
		},
		{
			name: "unknown trigger",
			mutate: func(args []string) []string {
				return replaceArgValue(args, "--trigger-kind", "arbitrary")
			},
			message: "unsupported --trigger-kind",
		},
		{
			name: "CPU duration below range",
			mutate: func(args []string) []string {
				return replaceArgValue(args, "--cpu-seconds", "0")
			},
			message: "--cpu-seconds must be an integer from 1 through 30",
		},
		{
			name: "non-loopback URL",
			mutate: func(args []string) []string {
				return replaceNthArgValue(args, "--node", 0, "http://192.0.2.1:5011")
			},
			message: "must be an explicit loopback HTTP base URL",
		},
		{
			name: "duplicate URL",
			mutate: func(args []string) []string {
				return replaceNthArgValue(args, "--node", 1, "http://127.0.0.1:5011")
			},
			message: "multiple --node values must be distinct",
		},
		{
			name: "invalid UTC bracket",
			mutate: func(args []string) []string {
				return replaceArgValue(args, "--current-utc", "2026-08-12T23:59:59Z")
			},
			message: "--previous-utc must be earlier than --current-utc",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := test.mutate(append([]string(nil), validArgs...))
			outDir := args[1]
			cmd := exec.Command("bash", append([]string{scriptPath}, args...)...)
			cmd.Dir = root
			cmd.Env = testEnvironmentWith("WK_BENCH_API_TOKEN", "local-threshold-pprof-test-token")
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), test.message) {
				t.Fatalf("invalid parameters were not rejected with %q: %v\n%s", test.message, err, output)
			}
			if _, err := os.Stat(outDir); !os.IsNotExist(err) {
				t.Fatalf("invalid parameters created artifact directory %s: %v", outDir, err)
			}
		})
	}
}

func TestLocalThresholdPprofScriptRejectsMissingOrUnsafeTokenBeforeCreatingArtifacts(t *testing.T) {
	root := repoRoot(t)
	scriptPath := filepath.Join(root, "scripts", "capture-wukongim-local-threshold-pprof.sh")
	phasePath := filepath.Join(t.TempDir(), "phase")
	if err := os.WriteFile(phasePath, []byte("measurement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		token   string
		message string
	}{
		{name: "missing", token: "", message: "WK_BENCH_API_TOKEN is required"},
		{name: "header injection", token: "valid\r\nX-Injected: true", message: "must not contain CR or LF"},
		{name: "leading whitespace", token: " secret", message: "must not have leading or trailing whitespace"},
		{name: "trailing whitespace", token: "secret ", message: "must not have leading or trailing whitespace"},
	} {
		t.Run(test.name, func(t *testing.T) {
			outDir := filepath.Join(t.TempDir(), "capture")
			cmd := exec.Command("bash", scriptPath,
				"--out-dir", outDir,
				"--phase-state-file", phasePath,
				"--trigger-kind", "actual_offered_ratio",
				"--trigger-observed-phase", "measurement",
				"--previous-utc", "2026-08-13T00:00:00Z",
				"--current-utc", "2026-08-13T00:00:01Z",
				"--node", "http://127.0.0.1:5011",
				"--node", "http://127.0.0.1:5012",
				"--node", "http://127.0.0.1:5013")
			cmd.Dir = root
			cmd.Env = testEnvironmentWith("WK_BENCH_API_TOKEN", test.token)
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), test.message) {
				t.Fatalf("unsafe token was not rejected with %q: %v\n%s", test.message, err, output)
			}
			if _, err := os.Stat(outDir); !os.IsNotExist(err) {
				t.Fatalf("unsafe token created artifact directory %s: %v", outDir, err)
			}
		})
	}
}

func testEnvironmentWith(name, value string) []string {
	prefix := name + "="
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, prefix) {
			environment = append(environment, entry)
		}
	}
	return append(environment, prefix+value)
}

func replaceArgValue(args []string, name, value string) []string {
	return replaceNthArgValue(args, name, 0, value)
}

func replaceNthArgValue(args []string, name string, occurrence int, value string) []string {
	seen := 0
	for index := 0; index+1 < len(args); index++ {
		if args[index] != name {
			continue
		}
		if seen == occurrence {
			args[index+1] = value
			return args
		}
		seen++
	}
	return args
}

func removeNthArgPair(args []string, name string, occurrence int) []string {
	seen := 0
	for index := 0; index+1 < len(args); index++ {
		if args[index] != name {
			continue
		}
		if seen == occurrence {
			return append(args[:index:index], args[index+2:]...)
		}
		seen++
	}
	return args
}
