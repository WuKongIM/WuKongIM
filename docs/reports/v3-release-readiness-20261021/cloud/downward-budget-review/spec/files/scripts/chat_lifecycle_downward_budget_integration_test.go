//go:build integration

package scripts_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChatLifecycleDownwardBudgetStageRejectsBeforeAnyExternalAction(t *testing.T) {
	root := repoRoot(t)
	for _, tc := range []struct{ limit, prior string }{{"", ""}, {"1500000001", ""}, {"1351044800", "100"}, {"150000000", ""}} {
		t.Run(tc.limit+tc.prior, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"gh", "ssh-keygen", "go", "wktool"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >>\"$SPY_LOG\"\nexit 91\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", filepath.Join(root, "scripts/chat-lifecycle/stage-orchestrate.sh"))
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "SPY_LOG="+filepath.Join(dir, "calls"), "GH_TOKEN=fixture", "GITHUB_REPOSITORY=WuKongIM/WuKongIM", "WK_CHAT_REQUEST_ID=budget-fixture", "WK_CHAT_SOURCE_SHA="+strings.Repeat("a", 40), "WK_CHAT_OPERATOR=tangtaoit", "WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY=fixture", "WK_CHAT_WRAPPING_PUBLIC_KEY=fixture", "WK_CHAT_TOOL="+filepath.Join(dir, "wktool"), "WK_CHAT_TEMPLATE="+filepath.Join(root, "configs/cloud/chat-lifecycle/rehearsal-v1.json"), "WK_CHAT_OUTPUT_DIR="+filepath.Join(dir, "output"), "WK_CHAT_WORK_DIR="+filepath.Join(dir, "work"), "WK_CHAT_REQUEST_BUDGET_LIMIT_MICROS="+tc.limit, "WK_CHAT_RELEASED_PRECLOCK_RUN_ID="+tc.prior)
			output, err := cmd.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 1 {
				t.Fatalf("budget admission did not reject: %v %s", err, output)
			}
			b, err := os.ReadFile(filepath.Join(dir, "output/request-budget.json"))
			if err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			if err = json.Unmarshal(b, &result); err != nil || result["reason"] != "request_budget_invalid" {
				t.Fatalf("wrong rejection: %s", b)
			}
			if calls, _ := os.ReadFile(filepath.Join(dir, "calls")); len(calls) != 0 {
				t.Fatalf("invalid budget reached external action: %s", calls)
			}
		})
	}
}
func TestChatLifecycleDownwardBudgetCannotResetSpentRequest(t *testing.T) {
	f := newReleasedPreclockFixture(t)
	f.env = append(f.env, "WK_CHAT_RELEASED_PRECLOCK_RUN_ID=", "WK_CHAT_REQUEST_BUDGET_LIMIT_MICROS=1351044800")
	os.WriteFile(filepath.Join(f.directory, "inventory.json"), []byte(`{"complete":true,"artifacts":[{"id":200,"name":"chat-lifecycle-rehearsal-cleanup-request-1","expired":false,"workflow_run":{"id":100}}]}`), 0600)
	r, _, err := f.run(t)
	if err == nil || r["reason"] != "request_already_spent" {
		t.Fatalf("lower cap reset request: %v %v", r, err)
	}
}

func TestChatLifecycleDownwardBudgetActivationEnvironmentPreservesAdmittedValues(t *testing.T) {
	source := string(readWorkflow(t, "cloud-deployment-activate.yml"))
	start := strings.Index(source, `          expires_at="$(jq -er .expires_at deployment-plan.json)"`)
	end := strings.Index(source, `          openssl req -x509`)
	if start < 0 || end <= start {
		t.Fatal("missing reviewed environment writer")
	}
	// Execute only the unchanged local JSON-to-environment transform, with public fixture strings.
	script := "set -euo pipefail\n" + source[start:end]
	dir := t.TempDir()
	doc := `{"expires_at":"2026-10-25T00:00:00Z","lease_created_at":"2026-10-21T00:00:00Z","budget":{"limit_micros":1351044800,"operational_stop_micros":1201044800,"committed_micros":74477600,"estimated_cost_micros":1000000000,"line_items":[{"kind":"postpaid_host_hour","role":"all","quantity":384,"cost_micros":1000000000}]}}`
	if err := os.WriteFile(filepath.Join(dir, "deployment-plan.json"), []byte(doc), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "manager_jwt=public-fixture", "users=[]", "bench_token=public-fixture", "worker_token=public-fixture", "demo_user=fixture", "demo_hash=fixture")
	output, err := cmd.CombinedOutput()
	if err != nil || len(output) != 0 {
		t.Fatalf("environment writer failed or logged credentials: %v %s", err, output)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "runtime-load/load.env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"WK_CHAT_BUDGET_LIMIT_MICROS=1351044800\n", "WK_CHAT_BUDGET_OPERATIONAL_STOP_MICROS=1201044800\n", "WK_CHAT_BUDGET_COMMITTED_MICROS=74477600\n", "WK_CHAT_BUDGET_ESTIMATED_MICROS=1000000000\n"} {
		if !strings.Contains(string(raw), value) {
			t.Fatalf("lost admitted envelope %q", value)
		}
	}
}
