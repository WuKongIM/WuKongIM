//go:build integration

package scripts_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The example cap preserves the reviewed 150 CNY cleanup reserve exactly.
const downwardConsumerHard int64 = 1351044800
const downwardConsumerStop int64 = 1201044800

type downwardConsumerFixture struct {
	root, handoff, final, transition, cleanup, publicKey string
	env                                                  []string
}

func newDownwardConsumerFixture(t *testing.T, stage string, hard, stop int64) *downwardConsumerFixture {
	t.Helper()
	_ = repoRoot(t)
	f := &downwardConsumerFixture{root: t.TempDir()}
	if err := os.Symlink(filepath.Join(repoRoot(t), "scripts"), filepath.Join(f.root, "scripts")); err != nil {
		t.Fatal(err)
	}
	f.handoff, f.final, f.transition, f.cleanup = filepath.Join(f.root, "handoff"), filepath.Join(f.root, "final"), filepath.Join(f.root, "transition"), filepath.Join(f.root, "cleanup")
	for _, dir := range []string{f.handoff, f.final, f.transition, f.cleanup, filepath.Join(f.root, "bin"), filepath.Join(f.root, "previous-final"), filepath.Join(f.handoff, "rehearsal")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	key := filepath.Join(f.root, "fixture-key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("fixture key: %v: %s", err, out)
	}
	f.publicKey = strings.TrimSpace(readFile(t, key+".pub"))
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	// Stub only the existing remote sidecar authority; budget remains the real local reader.
	writeFile(t, filepath.Join(f.root, "bin", "python3"), `#!/bin/bash
set -eu
if [[ "$1" == scripts/chat-lifecycle/authenticate-released-preclock.py && "$2" == validate ]]; then
  printf '%s\n' "$*" >>"$DOWNWARD_AUTH_CALLS"
  exit "${DOWNWARD_AUTH_STATUS:-0}"
fi
exec "$DOWNWARD_REAL_PYTHON" "$@"
`)
	writeFile(t, filepath.Join(f.root, "bin", "gh"), `#!/bin/bash
set -eu
[[ "$1" == api ]]
case "$2" in
  */actions/runs/100) path=chat-lifecycle-rehearsal-finalize.yml; event=schedule ;;
  */actions/runs/200) path=cloud-deployment-bundle.yml; event=workflow_dispatch ;;
  *) exit 91 ;;
esac
jq -n --arg path ".github/workflows/$path" --arg event "$event" '{repository:{full_name:"WuKongIM/WuKongIM"},head_repository:{full_name:"WuKongIM/WuKongIM"},event:$event,head_branch:"main",status:"completed",conclusion:"success",path:$path}'
`)
	for _, name := range []string{"python3", "gh"} {
		if err := os.Chmod(filepath.Join(f.root, "bin", name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.env = append(os.Environ(), "PATH="+filepath.Join(f.root, "bin")+":"+os.Getenv("PATH"), "DOWNWARD_REAL_PYTHON="+python,
		"DOWNWARD_AUTH_CALLS="+filepath.Join(f.root, "authority-calls"), "GITHUB_REPOSITORY=WuKongIM/WuKongIM", "WK_CHAT_REQUEST_ID=request-1", "RUNNER_TEMP="+f.root,
		"WK_CHAT_HANDOFF_DIR="+f.handoff, "WK_CHAT_FINAL_DIR="+f.final, "WK_CHAT_TRANSITION_DIR="+f.transition, "WK_CHAT_CLEANUP_DIR="+f.cleanup,
		"WK_CHAT_WRAPPING_PUBLIC_KEY="+f.publicKey, "GITHUB_ENV="+filepath.Join(f.root, "github-env"), "GITHUB_OUTPUT="+filepath.Join(f.root, "github-output"), "WK_CHAT_COMPLETE_DIR="+filepath.Join(f.root, "complete"))
	for _, dir := range []string{f.handoff, f.final, filepath.Join(f.root, "previous-final")} {
		prior := int64(0)
		if stage == "formal" {
			prior = 104477600
		}
		f.documents(t, dir, stage, hard, stop, prior, 1000000)
	}
	for _, dir := range []string{f.transition, filepath.Join(f.handoff, "rehearsal")} {
		f.documents(t, dir, "rehearsal", hard, stop, 0, 104477600)
	}
	f.json(t, filepath.Join(f.cleanup, "cleanup.json"), map[string]any{"zero_inventory": true})
	f.json(t, filepath.Join(f.cleanup, "zero-inventory.json"), map[string]any{"result": map[string]any{"zero_inventory": map[string]any{"selector": map[string]any{}, "observed_at": "2026-10-20T01:00:00Z"}}})
	return f
}

func (f *downwardConsumerFixture) json(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *downwardConsumerFixture) documents(t *testing.T, dir, stage string, hard, stop, prior, cost int64) {
	t.Helper()
	budget := map[string]any{"currency": "CNY", "limit_micros": hard, "operational_stop_micros": stop, "committed_micros": prior}
	source, bundle := strings.Repeat("a", 40), "sha256:"+strings.Repeat("b", 64)
	lease := map[string]any{"budget": budget, "request_id": "request-1", "lease_id": "request-1-" + stage + "-1", "repository": "WuKongIM/WuKongIM", "host_groups": []any{map[string]any{"role": "service", "count": 3}}, "provenance": map[string]any{"source_sha": source}}
	plan := map[string]any{"schema": "wukongim.chat_lifecycle.run_plan/v1", "stage": stage, "attempt": 1, "operational_stop_micros": stop, "lease_plan": lease, "bootstrap_access": map[string]any{"authorized_keys": []string{"deployment-fixture", f.publicKey}}}
	quote := map[string]any{"estimated_cost_micros": cost, "line_items": []any{map[string]any{"kind": "eip_retention_policy_risk_hour", "quantity": 1, "cost_micros": cost}}}
	f.json(t, filepath.Join(dir, "run-plan.json"), plan)
	f.json(t, filepath.Join(dir, "receipt.json"), map[string]any{"schema": "wukongim.cloud_lease.receipt/v1", "receipt": map[string]any{"budget": budget, "quote": quote, "state": "active", "request_id": "request-1", "lease_id": "request-1-" + stage + "-1", "created_at": "2026-10-20T00:00:00Z"}})
	f.json(t, filepath.Join(dir, "deployment-plan.json"), map[string]any{"schema": "wukongim.cloud_deployment.plan/v2", "request_id": "request-1", "lease_id": "request-1-" + stage + "-1", "budget": budget})
	f.json(t, filepath.Join(dir, "quote.json"), map[string]any{"quote": quote})
	f.json(t, filepath.Join(dir, "handoff.json"), map[string]any{"schema": "wukongim.chat_lifecycle." + stage + "_handoff/v1", "request_id": "request-1", "source_sha": source, "bundle_digest": bundle, "bundle_run_id": 200})
	f.json(t, filepath.Join(dir, "transition.json"), map[string]any{"schema": "wukongim.chat_lifecycle.formal_transition/v1", "from_stage": "rehearsal", "outcome": "rehearsal_pass", "zero_inventory": true, "request_id": "request-1", "source_sha": source, "bundle_digest": bundle, "codex_diagnostic_pubkey": f.publicKey, "committed_micros": cost})
	f.json(t, filepath.Join(dir, "rehearsal-result.json"), map[string]any{"schema": "wukongim.chat_lifecycle.rehearsal_result/v1", "outcome": "rehearsal_pass"})
	f.json(t, filepath.Join(dir, "finalization.json"), map[string]any{"schema": "wukongim.chat_lifecycle." + stage + "_finalization/v1", "request_id": "request-1", "outcome": "rehearsal_pass", "cause": "rehearsal_completed"})
	f.json(t, filepath.Join(dir, "cleanup.json"), map[string]any{"schema": "wukongim.chat_lifecycle.rehearsal_cleanup/v1", "request_id": "request-1", "zero_inventory": true})
	f.json(t, filepath.Join(dir, "zero-inventory.json"), map[string]any{"result": map[string]any{"zero_inventory": map[string]any{"selector": map[string]any{}, "observed_at": "2026-10-20T01:00:00Z"}}})
	f.json(t, filepath.Join(dir, "release-selector.json"), map[string]any{"selector": map[string]any{}})
	f.json(t, filepath.Join(dir, "evidence", "manifest.json"), map[string]any{"schema": "wukongim.chat_lifecycle.terminal_evidence/v1", "stage": stage, "complete": true})
	f.json(t, filepath.Join(dir, "final.json"), map[string]any{"resources": map[string]any{"capacity": map[string]any{"network_transmit_bytes": 0}}})
	f.json(t, filepath.Join(dir, "encrypted-deployment-identity.json"), map[string]any{"deployment_public_key": "deployment-fixture"})
	for _, name := range []string{"deployment-outcome.json", "run-start.json"} {
		f.json(t, filepath.Join(dir, name), map[string]any{})
	}
}

func (f *downwardConsumerFixture) run(t *testing.T, script string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Dir, cmd.Env = f.root, f.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestChatLifecycleDownwardBudgetFormalAdmissionActualWorkflowShell(t *testing.T) {
	_ = repoRoot(t)
	steps := releasedPreclockConsumerSteps(t, "chat-lifecycle-formal.yml", "orchestrate")
	_, auth := releasedPreclockConsumerStepAt(t, steps, "Authenticate transition, bundle, aggregate ledger, and SSH identities")
	for _, tc := range []struct {
		name             string
		hard, stop, cost int64
		corrupt          string
		wantOK           bool
	}{
		{"lower", downwardConsumerHard, downwardConsumerStop, 104477600, "", true},
		{"default", 1500000000, 1350000000, 104477600, "", true},
		{"stop_minus_one", downwardConsumerHard, downwardConsumerStop, 1201044799, "", true},
		{"at_stop", downwardConsumerHard, downwardConsumerStop, 1201044800, "", false},
		{"above_stop", downwardConsumerHard, downwardConsumerStop, 1201044801, "", false},
		{"missing_plan_cap", downwardConsumerHard, downwardConsumerStop, 104477600, "plan", false},
		{"mismatched_receipt", downwardConsumerHard, downwardConsumerStop, 104477600, "receipt", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDownwardConsumerFixture(t, "rehearsal", tc.hard, tc.stop)
			f.documents(t, f.transition, "rehearsal", tc.hard, tc.stop, 0, tc.cost)
			if tc.corrupt == "plan" {
				f.json(t, filepath.Join(f.transition, "run-plan.json"), map[string]any{"schema": "wukongim.chat_lifecycle.run_plan/v1", "stage": "rehearsal", "attempt": 1, "lease_plan": map[string]any{"budget": map[string]any{"committed_micros": 0}, "host_groups": []any{map[string]any{"role": "service", "count": 3}}}})
			} else if tc.corrupt == "receipt" {
				path := filepath.Join(f.transition, "receipt.json")
				var receipt map[string]any
				if err := json.Unmarshal([]byte(readFile(t, path)), &receipt); err != nil {
					t.Fatal(err)
				}
				receipt["receipt"].(map[string]any)["budget"].(map[string]any)["limit_micros"] = 1500000000
				f.json(t, path, receipt)
			}
			out, err := f.run(t, strings.ReplaceAll(auth.Run, "${{ matrix.transition_run_id }}", "100"))
			if (err == nil) != tc.wantOK {
				t.Fatalf("admission wantOK=%v: %v: %s", tc.wantOK, err, out)
			}
			data, _ := os.ReadFile(filepath.Join(f.root, "github-env"))
			if tc.wantOK && !strings.Contains(string(data), fmt.Sprintf("WK_CHAT_REQUEST_BUDGET_LIMIT_MICROS=%d\n", tc.hard)) {
				t.Fatalf("authenticated cap not propagated: %s", data)
			}
			if !tc.wantOK && strings.Contains(string(data), "WK_CHAT_REQUEST_BUDGET_LIMIT_MICROS=") {
				t.Fatalf("rejected evidence exported a procurement cap: %s", data)
			}
		})
	}
}

func TestChatLifecycleDownwardBudgetTransitionSelector(t *testing.T) {
	_ = repoRoot(t)
	for _, tc := range []struct {
		cost   int64
		wantOK bool
	}{{1201044799, true}, {1201044800, false}, {1201044801, false}} {
		t.Run(strconv.FormatInt(tc.cost, 10), func(t *testing.T) {
			f := newDownwardConsumerFixture(t, "rehearsal", downwardConsumerHard, downwardConsumerStop)
			f.documents(t, f.transition, "rehearsal", downwardConsumerHard, downwardConsumerStop, 0, tc.cost)
			cmd := exec.Command("jq", "-er", "--arg", "request", "request-1", "--arg", "source", strings.Repeat("a", 40), "--arg", "bundle", "sha256:"+strings.Repeat("b", 64), "--argjson", "operational_stop", "1201044800", "-f", filepath.Join(repoRoot(t), "scripts/chat-lifecycle/select-formal-transition-committed.jq"), filepath.Join(f.transition, "transition.json"))
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.wantOK {
				t.Fatalf("selector boundary wantOK=%v: %v: %s", tc.wantOK, err, out)
			}
			if tc.wantOK && strings.TrimSpace(string(out)) != "1201044799" {
				t.Fatalf("wrong commitment: %s", out)
			}
		})
	}
}

func (f *downwardConsumerFixture) changeBudgetField(t *testing.T, dir, document, field string, value any) {
	t.Helper()
	path := filepath.Join(dir, document)
	var data map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &data); err != nil {
		t.Fatal(err)
	}
	container := data
	if document == "run-plan.json" {
		container = data["lease_plan"].(map[string]any)
	} else if document == "receipt.json" {
		container = data["receipt"].(map[string]any)
	}
	container["budget"].(map[string]any)[field] = value
	f.json(t, path, data)
}

func TestChatLifecycleDownwardBudgetFinalizerAuthenticationActualWorkflowShell(t *testing.T) {
	_ = repoRoot(t)
	for _, stage := range []string{"rehearsal", "formal"} {
		steps := releasedPreclockConsumerSteps(t, "chat-lifecycle-"+stage+"-finalize.yml", "finalize")
		_, auth := releasedPreclockConsumerStepAt(t, steps, "continuation")
		_, cleanup := releasedPreclockConsumerStepAt(t, steps, "cleanup")
		for _, previous := range []string{"false", "true"} {
			for _, corrupt := range []string{"", "handoff_receipt", "handoff_deployment", "final_receipt", "final_deployment", "final_higher", "rehearsal_receipt", "sidecar_authority"} {
				t.Run(stage+"/previous_"+previous+"/"+corrupt, func(t *testing.T) {
					f := newDownwardConsumerFixture(t, stage, downwardConsumerHard, downwardConsumerStop)
					selected := f.final
					if previous == "true" {
						selected = filepath.Join(f.root, "previous-final")
					}
					switch corrupt {
					case "handoff_receipt":
						f.changeBudgetField(t, f.handoff, "receipt.json", "limit_micros", 1500000000)
					case "handoff_deployment":
						f.changeBudgetField(t, f.handoff, "deployment-plan.json", "operational_stop_micros", 1350000000)
					case "final_receipt":
						f.changeBudgetField(t, selected, "receipt.json", "currency", "USD")
					case "final_deployment":
						f.changeBudgetField(t, selected, "deployment-plan.json", "committed_micros", 1)
					case "final_higher":
						prior := int64(0)
						if stage == "formal" {
							prior = 104477600
						}
						f.documents(t, selected, stage, 1500000000, 1350000000, prior, 1000000)
					case "rehearsal_receipt":
						if stage == "rehearsal" {
							f.changeBudgetField(t, f.handoff, "receipt.json", "committed_micros", 1)
						} else {
							f.changeBudgetField(t, filepath.Join(f.handoff, "rehearsal"), "receipt.json", "limit_micros", 1500000000)
						}
					case "sidecar_authority":
						f.env = append(f.env, "DOWNWARD_AUTH_STATUS=19")
					}
					// Execute the real zero-proof classifier first, as the workflow does after Release.
					out, err := f.run(t, strings.ReplaceAll(cleanup.Run, "${{ steps.handoff.outputs.mode }}", "run"))
					if err != nil {
						t.Fatalf("cleanup classification: %v: %s", err, out)
					}
					out, err = f.run(t, strings.ReplaceAll(auth.Run, "${{ steps.existing.outputs.ready }}", previous))
					if (err == nil) != (corrupt == "") {
						t.Fatalf("budget qualification corrupt=%s: %v: %s", corrupt, err, out)
					}
					if got := readFile(t, filepath.Join(f.root, "github-output")); !strings.Contains(got, "zero=true\n") {
						t.Fatalf("validation lost cleanup proof: %s", got)
					}
					if _, err := os.Stat(filepath.Join(f.cleanup, "zero-inventory.json")); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestChatLifecycleDownwardBudgetRehearsalTransitionBoundaryActualWorkflowShell(t *testing.T) {
	_ = repoRoot(t)
	steps := releasedPreclockConsumerSteps(t, "chat-lifecycle-rehearsal-finalize.yml", "finalize")
	_, auth := releasedPreclockConsumerStepAt(t, steps, "continuation")
	_, transition := releasedPreclockConsumerStepAt(t, steps, "transition")
	for _, previous := range []string{"false", "true"} {
		for _, tc := range []struct {
			name             string
			hard, stop, cost int64
			wantOK           bool
		}{
			{"stop_minus_one", downwardConsumerHard, downwardConsumerStop, 1201044799, true},
			{"at_stop", downwardConsumerHard, downwardConsumerStop, 1201044800, false},
			{"above_stop", downwardConsumerHard, downwardConsumerStop, 1201044801, false},
			{"default", 1500000000, 1350000000, 1349999999, true},
		} {
			t.Run("previous_"+previous+"/"+tc.name, func(t *testing.T) {
				f := newDownwardConsumerFixture(t, "rehearsal", tc.hard, tc.stop)
				selected := f.final
				if previous == "true" {
					selected = filepath.Join(f.root, "previous-final")
				}
				f.documents(t, selected, "rehearsal", tc.hard, tc.stop, 0, tc.cost)
				// Remove fixture transition so acceptance must create a fresh artifact.
				if err := os.RemoveAll(f.transition); err != nil {
					t.Fatal(err)
				}
				out, err := f.run(t, strings.ReplaceAll(auth.Run, "${{ steps.existing.outputs.ready }}", previous))
				if err != nil {
					t.Fatalf("fixture budget authentication: %v: %s", err, out)
				}
				out, err = f.run(t, strings.ReplaceAll(transition.Run, "${{ steps.existing.outputs.ready }}", previous))
				if (err == nil) != tc.wantOK {
					t.Fatalf("transition boundary wantOK=%v: %v: %s", tc.wantOK, err, out)
				}
				_, statErr := os.Stat(filepath.Join(f.transition, "transition.json"))
				if (statErr == nil) != tc.wantOK {
					t.Fatalf("transition artifact at boundary: %v", statErr)
				}
				if tc.wantOK {
					if readFile(t, filepath.Join(f.transition, "run-plan.json")) != readFile(t, filepath.Join(selected, "run-plan.json")) {
						t.Fatal("transition changed the authenticated full Run Plan")
					}
				}
			})
		}
	}
}

func TestChatLifecycleDownwardBudgetFormalCompleteBoundaryActualWorkflowShell(t *testing.T) {
	_ = repoRoot(t)
	steps := releasedPreclockConsumerSteps(t, "chat-lifecycle-formal-finalize.yml", "finalize")
	_, auth := releasedPreclockConsumerStepAt(t, steps, "continuation")
	_, complete := releasedPreclockConsumerStepAt(t, steps, "complete")
	for _, previous := range []string{"false", "true"} {
		for _, tc := range []struct {
			name              string
			hard, stop, quote int64
			wantOK            bool
		}{
			{"hard_minus_one", downwardConsumerHard, downwardConsumerStop, 1246567199, true},
			{"at_hard", downwardConsumerHard, downwardConsumerStop, 1246567200, true},
			{"above_hard", downwardConsumerHard, downwardConsumerStop, 1246567201, false},
			{"default", 1500000000, 1350000000, 1395522400, true},
		} {
			t.Run("previous_"+previous+"/"+tc.name, func(t *testing.T) {
				f := newDownwardConsumerFixture(t, "formal", tc.hard, tc.stop)
				selected := f.final
				if previous == "true" {
					selected = filepath.Join(f.root, "previous-final")
				}
				f.documents(t, selected, "formal", tc.hard, tc.stop, 104477600, tc.quote)
				out, err := f.run(t, strings.ReplaceAll(auth.Run, "${{ steps.existing.outputs.ready }}", previous))
				if err != nil {
					t.Fatalf("fixture budget authentication: %v: %s", err, out)
				}
				out, err = f.run(t, strings.ReplaceAll(complete.Run, "${{ steps.existing.outputs.ready }}", previous))
				if (err == nil) != tc.wantOK {
					t.Fatalf("aggregate boundary wantOK=%v: %v: %s", tc.wantOK, err, out)
				}
				_, statErr := os.Stat(filepath.Join(f.root, "complete", "manifest.json"))
				if (statErr == nil) != tc.wantOK {
					t.Fatalf("qualified manifest at boundary: %v", statErr)
				}
			})
		}
	}
}
