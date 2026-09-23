//go:build integration

package scripts_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Exercise the same Deployment entry as local repair and the Workflow. The
// adapters execute real processes but never contact a host or provider.
func TestCloudDeploymentExecution(t *testing.T) {
	for _, mode := range []string{"success", "collector retry", "gate retry", "activation failure", "invalid receipt", "collector timeout", "gate timeout", "typed failure then unavailable", "stale evidence", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root := repoRoot(t)
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			write := func(name, body string) string {
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			}
			plan := write("plan.json", `{"plan_digest":"sha256:`+strings.Repeat("a", 64)+`"}`)
			lease := write("lease.json", `{}`)
			manifest := write("manifest.json", `{}`)
			credentials := write("credentials", "export WK_DEPLOY_TEST_CREDENTIAL=private-canary\n")
			outcome := write("outcome.json", `{"passed":true,"receipt":{"schema":"wukongim.cloud_deployment.receipt/v2"}}`)
			snapshot := write("snapshot.json", `{"stale":true}`)
			failure := filepath.Join(dir, "failure.json")
			lastGate := write("last-gate", "ready\n")
			log := filepath.Join(dir, "calls")
			pidPath := filepath.Join(dir, "child.pid")
			t.Cleanup(func() {
				if raw, err := os.ReadFile(pidPath); err == nil {
					if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
				}
			})
			writeFakeDeploymentCommand(t, bin, "ssh-writer", `#!/usr/bin/env bash
set -euo pipefail
printf 'ssh-config\n' >>"$WK_TEST_CALL_LOG"
`)
			writeFakeDeploymentCommand(t, bin, "activate", `#!/usr/bin/env bash
set -euo pipefail
printf 'activate\n' >>"$WK_TEST_CALL_LOG"
if [[ "$WK_TEST_MODE" == 'activation failure' ]]; then
  "$WK_TEST_FAILURE_WRITER" "$WK_CLOUD_FAILURE_OUTPUT" credential_cleanup_failed services_active service-2 'cleanup failed' 'staging cleanup is unconfirmed'
  exit 42
fi
printf 'services_active\n' >"$WK_CLOUD_LAST_GATE_OUTPUT"
rm -f "$WK_CLOUD_FAILURE_OUTPUT"
`)
			writeFakeDeploymentCommand(t, bin, "collector", `#!/usr/bin/env bash
set -euo pipefail
[[ "$WK_DEPLOY_TEST_CREDENTIAL" == private-canary ]]
[[ ! -e "$WK_CLOUD_READINESS_OUTPUT" ]]
printf 'collect\n' >>"$WK_TEST_CALL_LOG"
count=$(grep -c '^collect$' "$WK_TEST_CALL_LOG")
if [[ "$WK_TEST_MODE" == 'collector retry' && "$count" == 1 ]]; then exit 1; fi
if [[ "$WK_TEST_MODE" == 'typed failure then unavailable' && "$count" != 1 ]] || [[ "$WK_TEST_MODE" == 'stale evidence' ]]; then exit 1; fi
if [[ "$WK_TEST_MODE" == 'collector timeout' || "$WK_TEST_MODE" == cancel ]]; then
  trap 'exit 0' TERM
  (trap '' TERM; exec sleep 60) &
  printf '%s\n' "$!" >"$WK_TEST_CHILD_PID"
  wait
fi
printf '{"fresh":true}\n' >"$WK_CLOUD_READINESS_OUTPUT"
`)
			writeFakeDeploymentCommand(t, bin, "gate", `#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == deployment-gate && "$2" == --lease-receipt && "$3" == "$WK_CLOUD_LEASE_RECEIPT" && "$4" == --plan && "$5" == "$WK_CLOUD_DEPLOYMENT_PLAN" && "$6" == --bundle-manifest && "$7" == "$WK_CLOUD_BUNDLE_MANIFEST" && "$8" == --snapshot && "$9" == "$WK_CLOUD_READINESS_OUTPUT" ]]
jq -e '.fresh == true' "$9" >/dev/null
printf 'gate\n' >>"$WK_TEST_CALL_LOG"
count=$(grep -c '^gate$' "$WK_TEST_CALL_LOG")
if [[ "$WK_TEST_MODE" == 'gate timeout' ]]; then sleep 60; fi
if [[ "$WK_TEST_MODE" == 'gate retry' && "$count" == 1 ]] || [[ "$WK_TEST_MODE" == 'typed failure then unavailable' ]]; then
  printf '{"passed":false,"failure":{"schema":"wukongim.cloud_deployment.failure/v1","code":"cluster_membership_unready","last_completed_gate":"services_active","evidence":["cluster is not ready"]}}\n'
  exit 1
fi
digest=$(jq -r .plan_digest "$WK_CLOUD_DEPLOYMENT_PLAN")
[[ "$WK_TEST_MODE" != 'invalid receipt' ]] || digest=wrong-generation
jq -n --arg digest "$digest" '{passed:true,receipt:{schema:"wukongim.cloud_deployment.receipt/v2",deployment_plan_digest:$digest}}'
`)
			readinessSeconds := "2"
			// Successful retries must tolerate interpreter startup under the full
			// suite; failure cases retain a short deadline to exercise expiry.
			if mode == "success" || mode == "collector retry" || mode == "gate retry" {
				readinessSeconds = "10"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", filepath.Join(root, "scripts", "cloud-deployment", "deploy.sh"))
			cmd.WaitDelay = time.Second
			cmd.Env = append(os.Environ(),
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"WK_CLOUD_DEPLOYMENT_PLAN="+plan, "WK_CLOUD_LEASE_RECEIPT="+lease, "WK_CLOUD_BUNDLE_MANIFEST="+manifest,
				"WK_CLOUD_READINESS_CREDENTIALS="+credentials, "WK_CLOUD_READINESS_OUTPUT="+snapshot,
				"WK_CLOUD_OUTCOME_OUTPUT="+outcome, "WK_CLOUD_FAILURE_OUTPUT="+failure, "WK_CLOUD_LAST_GATE_OUTPUT="+lastGate,
				"WK_CLOUD_SSH_CONFIG_WRITER="+filepath.Join(bin, "ssh-writer"), "WK_CLOUD_ACTIVATOR="+filepath.Join(bin, "activate"),
				"WK_CLOUD_READINESS_COLLECTOR="+filepath.Join(bin, "collector"), "WK_CLOUD_GATE_TOOL="+filepath.Join(bin, "gate"),
				"WK_CLOUD_READINESS_TIMEOUT_SECONDS="+readinessSeconds, "WK_CLOUD_READINESS_POLL_SECONDS=0.05",
				"WK_TEST_MODE="+mode, "WK_TEST_CALL_LOG="+log, "WK_TEST_CHILD_PID="+pidPath,
				"WK_TEST_FAILURE_WRITER="+filepath.Join(root, "scripts", "cloud-deployment", "write-deployment-failure.sh"))
			outputPath := write("command.log", "")
			outputFile, err := os.OpenFile(outputPath, os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer outputFile.Close()
			cmd.Stdout = outputFile
			cmd.Stderr = outputFile
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			if mode == "cancel" {
				deadline := time.Now().Add(5 * time.Second)
				for {
					if _, err := os.Stat(pidPath); err == nil {
						break
					}
					if time.Now().After(deadline) {
						cancel()
						t.Fatal("collector did not start")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			}
			err = <-done
			logs, _ := os.ReadFile(outputPath)
			success := mode == "success" || mode == "collector retry" || mode == "gate retry"
			if success && err != nil {
				t.Fatalf("deployment failed: %v\n%s", err, logs)
			}
			if !success && err == nil {
				t.Fatalf("failure reported success: %s", logs)
			}
			if mode == "cancel" {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 143 {
					t.Fatalf("cancel result: %v\n%s", err, logs)
				}
			}
			body, readErr := os.ReadFile(outcome)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.Contains(string(body), "private-canary") {
				t.Fatal("credentials escaped into outcome")
			}
			var got struct {
				Passed  bool `json:"passed"`
				Receipt *struct {
					Schema string `json:"schema"`
				} `json:"receipt"`
				Failure *struct {
					Code string `json:"code"`
					Gate string `json:"last_completed_gate"`
					Role string `json:"host_role"`
				} `json:"failure"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("outcome %s: %v", body, err)
			}
			if got.Passed != success {
				t.Fatalf("outcome=%s; logs=%s", body, logs)
			}
			calls, _ := os.ReadFile(log)
			if !strings.HasPrefix(string(calls), "ssh-config\nactivate\n") || strings.Count(string(calls), "activate\n") != 1 {
				t.Fatalf("activation replayed or unordered: %s", calls)
			}
			if success {
				if got.Receipt == nil || got.Receipt.Schema != "wukongim.cloud_deployment.receipt/v2" {
					t.Fatalf("receipt=%s", body)
				}
				gate, _ := os.ReadFile(lastGate)
				if string(gate) != "ready\n" {
					t.Fatalf("last gate=%q", gate)
				}
				if _, err := os.Stat(failure); !os.IsNotExist(err) {
					t.Fatal("success retained failure guard")
				}
			} else {
				want := "readiness_evidence_invalid"
				if mode == "activation failure" {
					want = "credential_cleanup_failed"
					if strings.Contains(string(calls), "collect") {
						t.Fatal("continued after activation failure")
					}
				}
				if mode == "typed failure then unavailable" {
					want = "cluster_membership_unready"
				}
				if got.Failure == nil || got.Failure.Code != want || got.Failure.Gate != "services_active" {
					t.Fatalf("failure=%s", body)
				}
			}
			if mode == "collector retry" && strings.Count(string(calls), "gate\n") != 1 {
				t.Fatalf("gate consumed failed snapshot: %s", calls)
			}
			if mode == "stale evidence" && strings.Contains(string(calls), "gate\n") {
				t.Fatalf("gate consumed stale snapshot: %s", calls)
			}
			info, _ := os.Stat(outcome)
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("outcome mode=%v", info.Mode())
			}
			if raw, err := os.ReadFile(pidPath); err == nil {
				pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
				if err != nil {
					t.Fatal(err)
				}
				// Reaped processes and short-lived zombies are both non-running.
				state, _ := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
				if value := strings.TrimSpace(string(state)); value != "" && !strings.HasPrefix(value, "Z") {
					t.Fatalf("owned collector descendant still running: pid=%d state=%s", pid, value)
				}
			}
		})
	}
}

func TestCloudDeploymentPortableTimeoutEscalates(t *testing.T) {
	for _, cancelCommand := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelCommand), func(t *testing.T) {
			dir := t.TempDir()
			pidPath := filepath.Join(dir, "child.pid")
			writeFakeDeploymentCommand(t, dir, "stubborn", `#!/usr/bin/env bash
trap '' TERM
printf '%s\n' "$$" >"$1"
exec sleep 60
`)
			t.Cleanup(func() {
				if raw, err := os.ReadFile(pidPath); err == nil {
					if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			duration := "1s"
			if cancelCommand {
				duration = "60s"
			}
			cmd := exec.CommandContext(ctx, filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "portable-timeout.sh"),
				"--kill-after=1s", duration, filepath.Join(dir, "stubborn"), pidPath)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			if cancelCommand {
				deadline := time.Now().Add(5 * time.Second)
				for {
					if _, err := os.Stat(pidPath); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("command did not start")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			}
			err := cmd.Wait()
			want := 124
			if cancelCommand {
				want = 143
			}
			var status *exec.ExitError
			if !errors.As(err, &status) || status.ExitCode() != want {
				t.Fatalf("timeout result=%v, want exit %d", err, want)
			}
			raw, err := os.ReadFile(pidPath)
			if err != nil {
				t.Fatal(err)
			}
			state, _ := exec.Command("ps", "-p", strings.TrimSpace(string(raw)), "-o", "stat=").Output()
			if value := strings.TrimSpace(string(state)); value != "" && !strings.HasPrefix(value, "Z") {
				t.Fatalf("stubborn child still running: %s", state)
			}
		})
	}
}
