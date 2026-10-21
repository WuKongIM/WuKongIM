package scripts_test

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatLifecyclePreClockSafeUnitMetadata(t *testing.T) {
	helper := filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "pre-clock-diagnostic.py")
	for _, tc := range []struct {
		name, input string
		valid       bool
	}{
		{"terminal", "ActiveState=failed\nJob=0\nLoadState=loaded\nSubState=failed\nResult=exit-code\nExecMainCode=1\nExecMainStatus=2\n", true},
		{"secret value", "ActiveState=failed\nJob=0\nLoadState=loaded\nSubState=failed\nResult=secret-canary-do-not-publish\nExecMainCode=1\nExecMainStatus=2\n", false},
		{"invalid job", "ActiveState=active\nJob=secret-canary-do-not-publish\n", false},
		{"duplicate", "ActiveState=active\nActiveState=failed\nJob=0\n", false},
		{"oversize", strings.Repeat("secret-canary-do-not-publish", 500), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("python3", "-B", helper, "state")
			cmd.Stdin = strings.NewReader(tc.input)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("metadata CLI failed: %v: %s", err, out)
			}
			var got struct {
				Valid      bool           `json:"valid"`
				Bytes      int            `json:"bytes"`
				SHA        string         `json:"sha256"`
				Properties map[string]any `json:"properties"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			if got.Valid != tc.valid || got.Bytes != len(tc.input) || len(got.SHA) != 64 {
				t.Fatalf("wrong metadata: %s", out)
			}
			if strings.Contains(string(out), "secret-canary-do-not-publish") {
				t.Fatal("raw input escaped diagnostic boundary")
			}
			if tc.valid && (got.Properties["ActiveState"] != "failed" || got.Properties["ExecMainStatus"] != float64(2)) {
				t.Fatalf("safe fields lost: %s", out)
			}
		})
	}
}

func TestChatLifecyclePreClockSummaryDiagnosticKeepsLegacyDecision(t *testing.T) {
	classifier := filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "classify-pre-clock-summary.sh")
	for _, tc := range []struct {
		name, summary, code, preflight string
		accepted                       bool
	}{
		{"preflight", "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=preflight preflight_code=target_config report=unavailable", "preflight", "target_config", false},
		{"runtime", "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=runtime worker_runtime_code=engine_cpu_saturated preflight_code= report=unavailable", "runtime", "", true},
		{"unknown field", "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=preflight preflight_code=secret_canary report=unavailable", "", "", false},
		{"extra line", "chat-lifecycle outcome=harness_invalid cause=invalid_observation coordinator_code=setup preflight_code= report=unavailable\nsecret_canary", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("bash", classifier, "--diagnostic-json", tc.summary)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("diagnostic CLI failed: %v: %s", err, out)
			}
			var got struct {
				Schema      string `json:"schema"`
				Coordinator string `json:"coordinator_code"`
				Preflight   string `json:"preflight_code"`
				SHA         string `json:"sha256"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			if got.Schema != "wukongim.chat_lifecycle.pre_clock_summary/v1" || got.Coordinator != tc.code || got.Preflight != tc.preflight || len(got.SHA) != 64 {
				t.Fatalf("wrong safe summary: %s", out)
			}
			if strings.Contains(string(out), "secret_canary") || strings.Contains(string(out), "outcome=") {
				t.Fatal("raw summary leaked")
			}
			legacy, legacyErr := exec.Command("bash", classifier, tc.summary).CombinedOutput()
			if (legacyErr == nil) != tc.accepted {
				t.Fatalf("legacy decision changed: %s / %v", legacy, legacyErr)
			}
		})
	}
}
