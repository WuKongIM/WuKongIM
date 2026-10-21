//go:build integration

package scripts_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type releasedPreclockFixture struct {
	directory string
	env       []string
	api       map[string]any
}

func newReleasedPreclockFixture(t *testing.T) *releasedPreclockFixture {
	t.Helper()
	_ = repoRoot(t) // Enter the repository process-fixture scheduler before creating deadlines.
	dir := t.TempDir()
	repo := map[string]any{"id": 638054601, "full_name": "WuKongIM/WuKongIM"}
	run := func(id, number int, sha, status, conclusion string) map[string]any {
		return map[string]any{"id": id, "workflow_id": 329938003, "run_number": number, "run_attempt": 1, "path": ".github/workflows/chat-lifecycle-rehearsal.yml", "head_branch": "main", "head_sha": sha, "event": "workflow_dispatch", "status": status, "conclusion": conclusion, "display_title": "Chat Lifecycle Rehearsal request-1", "repository": repo, "head_repository": repo}
	}
	f := &releasedPreclockFixture{directory: dir, api: map[string]any{
		"/repos/WuKongIM/WuKongIM/actions/runs/100": run(100, 32, strings.Repeat("a", 40), "completed", "failure"),
		"/repos/WuKongIM/WuKongIM/actions/runs/101": run(101, 33, strings.Repeat("b", 40), "in_progress", ""),
	}}
	stub := `#!/usr/bin/env python3
import os,sys,json,base64
with open(os.environ['WK_TEST_CALLS'],'a') as f:f.write(json.dumps(sys.argv[1:])+'\n')
a=sys.argv[1:]
if a[:5] != ['api','--hostname','github.com','--method','GET']:sys.exit(92)
p=a[5]
v=json.load(open(os.environ['WK_TEST_API'])).get(p)
if v is None:sys.exit(93)
if isinstance(v,dict) and '__bytes' in v:sys.stdout.buffer.write(base64.b64decode(v['__bytes']))
else:print(json.dumps(v))
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inventory.json"), []byte(`{"complete":true,"artifacts":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	f.env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "GH_TOKEN=fixture-not-a-credential", "GITHUB_REPOSITORY=WuKongIM/WuKongIM", "GITHUB_REPOSITORY_ID=638054601", "GITHUB_RUN_ID=101", "GITHUB_RUN_NUMBER=33", "GITHUB_RUN_ATTEMPT=1", "GITHUB_SHA="+strings.Repeat("b", 40), "WK_CHAT_SOURCE_SHA="+strings.Repeat("a", 40), "WK_CHAT_REQUEST_ID=request-1", "WK_CHAT_OPERATOR=tangtaoit", "WK_CHAT_RELEASED_PRECLOCK_RUN_ID=100", "WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY=fixture-key", "WK_TEST_CALLS="+filepath.Join(dir, "calls.jsonl"), "WK_TEST_API="+filepath.Join(dir, "api.json"))
	return f
}
func (f *releasedPreclockFixture) run(t *testing.T) (map[string]any, string, error) {
	t.Helper()
	b, err := json.Marshal(f.api)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(f.directory, "api.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", filepath.Join(repoRoot(t), "scripts/chat-lifecycle/authenticate-released-preclock.py"), "prepare", "--inventory", filepath.Join(f.directory, "inventory.json"), "--output", filepath.Join(f.directory, "output"))
	cmd.Env = f.env
	out, runErr := cmd.CombinedOutput()
	var result map[string]any
	if err = json.Unmarshal(out, &result); err != nil {
		t.Fatalf("helper must emit one controlled JSON receipt; err=%v output=%s", runErr, out)
	}
	calls, _ := os.ReadFile(filepath.Join(f.directory, "calls.jsonl"))
	return result, string(calls), runErr
}
func TestChatLifecycleReleasedPreclockRejectsSequenceBeforeArtifactRead(t *testing.T) {
	tests := []struct {
		name, reason string
		change       func(*releasedPreclockFixture)
	}{
		{"gap", "nonadjacent_run", func(f *releasedPreclockFixture) {
			f.api["/repos/WuKongIM/WuKongIM/actions/runs/101"].(map[string]any)["run_number"] = 34
			f.env = append(f.env, "GITHUB_RUN_NUMBER=34")
		}},
		{"current_rerun", "rerun_not_supported", func(f *releasedPreclockFixture) {
			f.api["/repos/WuKongIM/WuKongIM/actions/runs/101"].(map[string]any)["run_attempt"] = 2
			f.env = append(f.env, "GITHUB_RUN_ATTEMPT=2")
		}},
		{"predecessor_rerun", "rerun_not_supported", func(f *releasedPreclockFixture) {
			f.api["/repos/WuKongIM/WuKongIM/actions/runs/100"].(map[string]any)["run_attempt"] = 2
		}},
		{"different_workflow", "producer_identity", func(f *releasedPreclockFixture) {
			f.api["/repos/WuKongIM/WuKongIM/actions/runs/100"].(map[string]any)["workflow_id"] = 88
		}},
		{"environment_identity", "producer_identity", func(f *releasedPreclockFixture) { f.env = append(f.env, "GITHUB_RUN_NUMBER=34") }},
		{"api_unavailable", "authority_unavailable", func(f *releasedPreclockFixture) { delete(f.api, "/repos/WuKongIM/WuKongIM/actions/runs/100") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newReleasedPreclockFixture(t)
			tt.change(f)
			r, calls, err := f.run(t)
			if err == nil || r["status"] != "rejected" || r["reason"] != tt.reason {
				t.Fatalf("want controlled %s rejection, err=%v receipt=%v", tt.reason, err, r)
			}
			if strings.Contains(calls, "/artifacts") || strings.Contains(calls, "workflow") {
				t.Fatalf("rejected sequence reached artifact/procurement: %s", calls)
			}
		})
	}
}

func TestChatLifecycleReleasedPreclockFirstStartDoesNotResetSpentRequest(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		inventory    string
		wantOK       bool
	}{
		{"fresh", "", `{"complete":true,"artifacts":[]}`, true},
		{"partial", "inventory_incomplete", `{"complete":false,"artifacts":[]}`, false},
		{"spent_cleanup", "request_already_spent", `{"complete":true,"artifacts":[{"id":200,"name":"chat-lifecycle-rehearsal-cleanup-request-1","expired":false,"workflow_run":{"id":100}}]}`, false},
		{"spent_complete", "request_already_spent", `{"complete":true,"artifacts":[{"id":200,"name":"chat-lifecycle-rehearsal-complete-request-1","expired":false,"workflow_run":{"id":100}}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReleasedPreclockFixture(t)
			f.env = append(f.env, "WK_CHAT_RELEASED_PRECLOCK_RUN_ID=")
			if err := os.WriteFile(filepath.Join(f.directory, "inventory.json"), []byte(tc.inventory), 0600); err != nil {
				t.Fatal(err)
			}
			r, calls, err := f.run(t)
			if tc.wantOK {
				if err != nil || r["status"] != "first_start" || r["committed_micros"] != float64(0) || r["attempt"] != float64(1) {
					t.Fatalf("fresh start rejected: %v %v", err, r)
				}
			} else if err == nil || r["reason"] != tc.reason {
				t.Fatalf("want %s, got %v %v", tc.reason, err, r)
			}
			if strings.Contains(calls, "/zip") || strings.Contains(calls, "dispatch") {
				t.Fatalf("history guard reached side effects: %s", calls)
			}
		})
	}
}

func TestChatLifecycleReleasedPreclockFirstStartTrustDoesNotDependOnTerminalStatus(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		untrusted    string
	}{
		{"completed", "completed", ""},
		{"in_progress", "in_progress", ""},
		{"queued", "queued", ""},
		{"unrelated_workflow", "in_progress", "path"},
		{"fork", "in_progress", "head_repository"},
		{"different_repository", "in_progress", "repository"},
		{"non_main", "in_progress", "head_branch"},
		{"untrusted_event", "in_progress", "event"},
		{"invalid_head", "in_progress", "head_sha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReleasedPreclockFixture(t)
			f.env = append(f.env, "WK_CHAT_RELEASED_PRECLOCK_RUN_ID=")
			run := f.api["/repos/WuKongIM/WuKongIM/actions/runs/100"].(map[string]any)
			run["path"] = ".github/workflows/chat-lifecycle-rehearsal-finalize.yml"
			run["event"] = "schedule"
			run["status"] = tc.status
			if tc.untrusted != "" {
				switch tc.untrusted {
				case "repository", "head_repository":
					run[tc.untrusted] = map[string]any{"id": 99, "full_name": "unrelated/fork"}
				default:
					run[tc.untrusted] = "unrelated"
				}
			}
			inventory := `{"complete":true,"artifacts":[{"id":200,"name":"chat-lifecycle-rehearsal-cleanup-request-1","expired":false,"workflow_run":{"id":100}}]}`
			if err := os.WriteFile(filepath.Join(f.directory, "inventory.json"), []byte(inventory), 0600); err != nil {
				t.Fatal(err)
			}
			r, calls, err := f.run(t)
			if tc.untrusted == "" {
				if err == nil || r["status"] != "rejected" || r["reason"] != "request_already_spent" {
					t.Fatalf("trusted retained cleanup must block prior reset regardless of producer status: %v %v", err, r)
				}
			} else if err != nil || r["status"] != "first_start" || r["committed_micros"] != float64(0) {
				t.Fatalf("untrusted same-name artifact changed default path: %v %v", err, r)
			}
			if strings.Contains(calls, "/zip") || strings.Contains(calls, "dispatch") {
				t.Fatalf("spent guard must stop before artifact download or side effects: %s", calls)
			}
			if _, err := os.Stat(filepath.Join(f.directory, "output")); !os.IsNotExist(err) {
				t.Fatal("first-start guard unexpectedly wrote continuation evidence")
			}
		})
	}
}

func TestChatLifecycleReleasedPreclockRejectsTypedTerminalBeforeFiltering(t *testing.T) {
	for _, code := range []string{"runtime", "grant"} {
		t.Run(code, func(t *testing.T) {
			f := newReleasedPreclockFixture(t)
			f.fullEvidence(t, func(documents map[string]any) {
				documents["pre-clock-terminal.json"] = map[string]any{
					"schema": "wukongim.chat_lifecycle.pre_clock_terminal/v1", "request_id": "request-1",
					"stage": "rehearsal", "coordinator_code": code,
				}
			})
			r, _, err := f.run(t)
			if err == nil || r["status"] != "rejected" || r["reason"] != "measured_or_runtime_predecessor" {
				t.Fatalf("typed terminal must not be discarded or relabeled unknown: %v %v", err, r)
			}
			if _, err := os.Stat(filepath.Join(f.directory, "output")); !os.IsNotExist(err) {
				t.Fatal("known terminal predecessor wrote a continuation sidecar")
			}
		})
	}
}

func (f *releasedPreclockFixture) fullEvidence(t *testing.T, mutate func(map[string]any)) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts/testdata/chat-lifecycle-released-preclock/original.json"))
	if err != nil {
		t.Fatal(err)
	}
	var documents map[string]any
	if err = json.Unmarshal(raw, &documents); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(documents)
	}
	original := f.api["/repos/WuKongIM/WuKongIM/actions/runs/100"].(map[string]any)
	original["created_at"] = "2026-10-21T10:00:35Z"
	original["updated_at"] = "2026-10-21T10:19:39Z"
	current := f.api["/repos/WuKongIM/WuKongIM/actions/runs/101"].(map[string]any)
	current["created_at"] = "2026-10-21T12:00:00Z"
	for _, x := range []struct {
		id                      int
		path, title, start, end string
	}{{102, "cloud-lease-provision", "Cloud Lease Provision request-1", "2026-10-21T10:10:20Z", "2026-10-21T10:11:59Z"}, {103, "cloud-lease-release", "Cloud Lease Release request-1", "2026-10-21T10:18:12Z", "2026-10-21T10:19:24Z"}} {
		c := map[string]any{}
		for k, v := range original {
			c[k] = v
		}
		c["id"] = x.id
		c["workflow_id"] = x.id
		c["path"] = ".github/workflows/" + x.path + ".yml"
		c["display_title"] = x.title
		c["conclusion"] = "success"
		c["created_at"] = x.start
		c["updated_at"] = x.end
		f.api[fmt.Sprintf("/repos/WuKongIM/WuKongIM/actions/runs/%d", x.id)] = c
	}
	f.api["/repos/WuKongIM/WuKongIM/actions/runs/103/jobs?per_page=100&page=1"] = map[string]any{"total_count": 1, "jobs": []any{map[string]any{"run_id": 103, "run_attempt": 1, "head_sha": strings.Repeat("a", 40), "steps": []any{map[string]any{"name": "Release and retain residual evidence", "status": "completed", "conclusion": "success", "started_at": "2026-10-21T10:18:30Z", "completed_at": "2026-10-21T10:19:20Z"}}}}}
	f.api["/repos/WuKongIM/WuKongIM/compare/"+strings.Repeat("a", 40)+"..."+strings.Repeat("b", 40)] = map[string]any{"status": "ahead", "base_commit": map[string]any{"sha": strings.Repeat("a", 40)}, "merge_base_commit": map[string]any{"sha": strings.Repeat("a", 40)}}
	artifacts := []any{}
	add := func(id, run int, name string, docs map[string]any) {
		var buf bytes.Buffer
		w := zip.NewWriter(&buf)
		for n, v := range docs {
			entry, e := w.Create(n)
			if e != nil {
				t.Fatal(e)
			}
			b, _ := json.Marshal(v)
			if _, e = entry.Write(b); e != nil {
				t.Fatal(e)
			}
		}
		if err = w.Close(); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(buf.Bytes())
		a := map[string]any{"id": id, "name": name, "expired": false, "size_in_bytes": buf.Len(), "digest": fmt.Sprintf("sha256:%x", sum), "workflow_run": map[string]any{"id": run, "head_sha": strings.Repeat("a", 40)}}
		f.api[fmt.Sprintf("/repos/WuKongIM/WuKongIM/actions/artifacts/%d", id)] = a
		f.api[fmt.Sprintf("/repos/WuKongIM/WuKongIM/actions/artifacts/%d/zip", id)] = map[string]any{"__bytes": base64.StdEncoding.EncodeToString(buf.Bytes())}
		f.api[fmt.Sprintf("/repos/WuKongIM/WuKongIM/actions/runs/%d/artifacts?per_page=100&page=1", run)] = map[string]any{"total_count": 1, "artifacts": []any{a}}
		artifacts = append(artifacts, a)
	}
	add(200, 100, "chat-lifecycle-rehearsal-cleanup-request-1", documents)
	add(201, 102, "cloud-lease-provision-request-1", map[string]any{"quote.json": documents["attempt-1/quote.json"], "receipt.json": documents["attempt-1/receipt.json"]})
	add(202, 103, "cloud-lease-release-request-1", map[string]any{"release.json": documents["zero-inventory.json"]})
	b, _ := json.Marshal(map[string]any{"complete": true, "artifacts": artifacts})
	if err = os.WriteFile(filepath.Join(f.directory, "inventory.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	key := documents["attempt-1/run-plan.json"].(map[string]any)["bootstrap_access"].(map[string]any)["authorized_keys"].([]any)[0].(string)
	f.env = append(f.env, "WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY="+key)
}
func TestChatLifecycleReleasedPreclockAuthenticatesReleasedOriginalAndReservesFullQuote(t *testing.T) {
	f := newReleasedPreclockFixture(t)
	f.fullEvidence(t, nil)
	r, calls, err := f.run(t)
	if err != nil || r["status"] != "ready" || r["attempt"] != float64(2) || r["committed_micros"] != float64(74477600) {
		t.Fatalf("valid public predecessor rejected: %v %v", err, r)
	}
	var p map[string]any
	b, e := os.ReadFile(filepath.Join(f.directory, "output", "proof.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &p); e != nil {
		t.Fatal(e)
	}
	if p["failure_class"] != "unclassified_before_accepted_run_start" || p["cost_method"] != "full_admitted_quote_reserve" || p["next_attempt"] != float64(2) {
		t.Fatalf("unknown cause or full reserve was lost: %v", p)
	}
	if strings.Contains(calls, "POST") || strings.Contains(calls, "dispatch") {
		t.Fatalf("read-only helper mutated authority: %s", calls)
	}
}

func TestChatLifecycleReleasedPreclockRejectsBrokenPublicChain(t *testing.T) {
	tests := []struct {
		name, reason string
		change       func(map[string]any)
	}{
		{"nonzero_prior", "predecessor_not_original_rehearsal", func(d map[string]any) { d["attempt-1/run-plan.json"].(map[string]any)["attempt"] = 2 }},
		{"runtime_marker", "measured_or_runtime_predecessor", func(d map[string]any) { d["run-start.json"] = map[string]any{"accepted": true} }},
		{"sidecar", "measured_or_runtime_predecessor", func(d map[string]any) { d["continuation/proof.json"] = map[string]any{} }},
		{"incomplete_zero", "zero_incomplete", func(d map[string]any) {
			d["zero-inventory.json"].(map[string]any)["result"].(map[string]any)["zero_inventory"].(map[string]any)["scopes"] = []any{"instances"}
		}},
		{"zero_before_release", "zero_time_invalid", func(d map[string]any) {
			d["zero-inventory.json"].(map[string]any)["result"].(map[string]any)["zero_inventory"].(map[string]any)["observed_at"] = "2026-10-21T10:18:13Z"
		}},
		{"old_quote_expired", "cost_coverage_unproven", func(d map[string]any) {
			q := d["attempt-1/quote.json"].(map[string]any)["quote"].(map[string]any)
			q["valid_until"] = "2026-10-21T10:10:00Z"
			d["attempt-1/receipt.json"].(map[string]any)["receipt"].(map[string]any)["quote"] = q
		}},
		{"missing_cost_line", "cost_coverage_unproven", func(d map[string]any) {
			q := d["attempt-1/quote.json"].(map[string]any)["quote"].(map[string]any)
			q["line_items"] = q["line_items"].([]any)[:3]
			d["attempt-1/receipt.json"].(map[string]any)["receipt"].(map[string]any)["quote"] = q
		}},
		{"known_correctness", "measured_or_runtime_predecessor", func(d map[string]any) {
			d["deployment-repair-pending.json"].(map[string]any)["failure_code"] = "correctness"
		}},
		{"account_mismatch", "zero_incomplete", func(d map[string]any) {
			d["zero-inventory.json"].(map[string]any)["result"].(map[string]any)["zero_inventory"].(map[string]any)["account_id_hash"] = "sha256:" + strings.Repeat("e", 64)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newReleasedPreclockFixture(t)
			f.fullEvidence(t, tc.change)
			r, _, err := f.run(t)
			if err == nil || r["status"] != "rejected" || r["reason"] != tc.reason {
				t.Fatalf("want %s got %v %v", tc.reason, err, r)
			}
			if _, err := os.Stat(filepath.Join(f.directory, "output")); !os.IsNotExist(err) {
				t.Fatal("failed evidence emitted reusable sidecar")
			}
		})
	}
}

func TestChatLifecycleReleasedPreclockConsumerReauthenticatesSidecar(t *testing.T) {
	for _, kind := range []string{"valid_other_consumer_run", "missing_proof", "changed_reserve", "changed_raw_evidence", "changed_api_attempt", "changed_archive_digest"} {
		t.Run(kind, func(t *testing.T) {
			f := newReleasedPreclockFixture(t)
			f.fullEvidence(t, nil)
			r, _, err := f.run(t)
			if err != nil || r["status"] != "ready" {
				t.Fatalf("prepare: %v %v", err, r)
			}
			raw, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts/testdata/chat-lifecycle-released-preclock/original.json"))
			if err != nil {
				t.Fatal(err)
			}
			var d map[string]any
			_ = json.Unmarshal(raw, &d)
			plan := d["attempt-1/run-plan.json"].(map[string]any)
			plan["attempt"] = 2
			lease := plan["lease_plan"].(map[string]any)
			lease["lease_id"] = "request-1-rehearsal-2"
			lease["budget"].(map[string]any)["committed_micros"] = 74477600
			b, _ := json.Marshal(plan)
			runPlan := filepath.Join(f.directory, "new-run-plan.json")
			_ = os.WriteFile(runPlan, b, 0600)
			dir := filepath.Join(f.directory, "output")
			reason := ""
			switch kind {
			case "valid_other_consumer_run":
				f.api["/repos/WuKongIM/WuKongIM/actions/runs/101"].(map[string]any)["status"] = "completed"
				f.api["/repos/WuKongIM/WuKongIM/actions/runs/101"].(map[string]any)["conclusion"] = "success"
				f.api["/repos/WuKongIM/WuKongIM/actions/runs/101"].(map[string]any)["updated_at"] = "2026-10-21T12:10:00Z"
			case "missing_proof":
				_ = os.Remove(filepath.Join(dir, "proof.json"))
				reason = "sidecar_missing"
			case "changed_reserve":
				b, _ = os.ReadFile(filepath.Join(dir, "proof.json"))
				var p map[string]any
				_ = json.Unmarshal(b, &p)
				p["committed_micros"] = 1
				b, _ = json.Marshal(p)
				_ = os.WriteFile(filepath.Join(dir, "proof.json"), b, 0600)
				reason = "sidecar_identity"
			case "changed_raw_evidence":
				_ = os.WriteFile(filepath.Join(dir, "evidence", "original", "cleanup.json"), []byte("{}"), 0600)
				reason = "sidecar_integrity"
			case "changed_api_attempt":
				f.api["/repos/WuKongIM/WuKongIM/actions/runs/101"].(map[string]any)["run_attempt"] = 2
				reason = "rerun_not_supported"
			case "changed_archive_digest":
				f.api["/repos/WuKongIM/WuKongIM/actions/artifacts/201"].(map[string]any)["digest"] = "sha256:" + strings.Repeat("f", 64)
				reason = "artifact_integrity"
			}
			b, _ = json.Marshal(f.api)
			_ = os.WriteFile(filepath.Join(f.directory, "api.json"), b, 0600)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "python3", filepath.Join(repoRoot(t), "scripts/chat-lifecycle/authenticate-released-preclock.py"), "validate", "--directory", f.directory, "--run-plan", runPlan)
			// The contract directory holds a continuation/ subtree. Different consumer IDs
			// must not replace the original rehearsal producer carried in that proof.
			_ = os.Rename(dir, filepath.Join(f.directory, "continuation"))
			cmd.Env = append(f.env, "GITHUB_RUN_ID=999", "GITHUB_RUN_NUMBER=9", "GITHUB_SHA="+strings.Repeat("e", 40))
			out, e := cmd.CombinedOutput()
			var result map[string]any
			if err = json.Unmarshal(out, &result); err != nil {
				t.Fatalf("must return controlled JSON: %v %s", e, out)
			}
			if reason == "" {
				if e != nil || result["status"] != "ready" {
					t.Fatalf("valid carried proof: %v %v", e, result)
				}
			} else if e == nil || result["reason"] != reason {
				t.Fatalf("want %s got %v %v", reason, e, result)
			}
		})
	}
}

func TestChatLifecycleReleasedPreclockStageRejectsBeforeBundleKeyQuoteOrAcquire(t *testing.T) {
	f := newReleasedPreclockFixture(t)
	f.env = append(f.env, "WK_CHAT_RELEASED_PRECLOCK_RUN_ID=")
	_ = os.WriteFile(filepath.Join(f.directory, "inventory.json"), []byte(`{"complete":true,"artifacts":[{"id":200,"name":"chat-lifecycle-rehearsal-cleanup-request-1","expired":false,"workflow_run":{"id":100}}]}`), 0600)
	b, _ := json.Marshal(f.api)
	_ = os.WriteFile(filepath.Join(f.directory, "api.json"), b, 0600)
	scripts := filepath.Join(f.directory, "scripts", "chat-lifecycle")
	_ = os.MkdirAll(scripts, 0700)
	helper, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts/chat-lifecycle/authenticate-released-preclock.py"))
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(scripts, "authenticate-released-preclock.py"), helper, 0600)
	_ = os.WriteFile(filepath.Join(scripts, "comment-request-issue.sh"), []byte("#!/bin/bash\nprintf '{\"status\":\"ready\"}\\n'\n"), 0700)
	spy := []byte("#!/bin/bash\nprintf 'unexpected-paid-boundary\\n' >>\"$WK_TEST_CALLS\"\nexit 91\n")
	for _, n := range []string{"ssh-keygen", "wktool"} {
		_ = os.WriteFile(filepath.Join(f.directory, n), spy, 0700)
	}
	f.env = append(f.env, "WK_CHAT_ARTIFACT_INVENTORY="+filepath.Join(f.directory, "inventory.json"), "WK_CHAT_WRAPPING_PUBLIC_KEY=public-fixture", "WK_CHAT_TOOL="+filepath.Join(f.directory, "wktool"), "WK_CHAT_TEMPLATE="+filepath.Join(repoRoot(t), "configs/cloud/chat-lifecycle/rehearsal-v1.json"), "WK_CHAT_OUTPUT_DIR="+filepath.Join(f.directory, "stage-output"), "WK_CHAT_WORK_DIR="+filepath.Join(f.directory, "stage-work"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(repoRoot(t), "scripts/chat-lifecycle/stage-orchestrate.sh"))
	cmd.Dir = f.directory
	cmd.Env = f.env
	out, e := cmd.CombinedOutput()
	var receipt map[string]any
	b, err = os.ReadFile(filepath.Join(f.directory, "stage-output", "continuation-authentication.json"))
	if err != nil {
		t.Fatalf("missing exact procurement rejection receipt: %v; exit=%v output=%s", err, e, out)
	}
	_ = json.Unmarshal(b, &receipt)
	if e == nil || receipt["reason"] != "request_already_spent" {
		t.Fatalf("wrong stage failure: %v %v output=%s", e, receipt, out)
	}
	calls, _ := os.ReadFile(filepath.Join(f.directory, "calls.jsonl"))
	if strings.Contains(string(calls), "unexpected-paid-boundary") || strings.Contains(string(calls), "workflow") {
		t.Fatalf("failed admission reached paid/key boundary: %s", calls)
	}
}

func TestChatLifecycleReleasedPreclockReusesCompleteBoundedDiscoveryInventory(t *testing.T) {
	root := repoRoot(t)
	d := t.TempDir()
	_ = os.WriteFile(filepath.Join(d, "gh"), []byte("#!/bin/bash\nprintf '{\"artifacts\":[{\"id\":7,\"expired\":false,\"name\":\"unrelated\"}]}\\n'\n"), 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	matrix := filepath.Join(d, "matrix.json")
	inventory := filepath.Join(d, "inventory.json")
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(root, "scripts/chat-lifecycle/discover-active-handoffs.sh"), "", matrix, inventory)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+d+string(os.PathListSeparator)+os.Getenv("PATH"), "GH_TOKEN=fixture", "GITHUB_REPOSITORY=WuKongIM/WuKongIM")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("discovery: %v %s", err, out)
	}
	b, err := os.ReadFile(inventory)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Complete  bool             `json:"complete"`
		Artifacts []map[string]any `json:"artifacts"`
	}
	if err = json.Unmarshal(b, &v); err != nil || !v.Complete || len(v.Artifacts) != 1 || v.Artifacts[0]["id"] != float64(7) {
		t.Fatalf("incomplete shared inventory: %v %s", err, b)
	}
	b, _ = os.ReadFile(matrix)
	if !strings.Contains(string(b), `"include":[]`) {
		t.Fatalf("default active matrix changed: %s", b)
	}
}

func TestChatLifecycleReleasedPreclockRejectsRetainedRuntimeAndStopEvidence(t *testing.T) {
	for _, kind := range []string{"runtime_summary", "stop_marker", "malformed_schema"} {
		t.Run(kind, func(t *testing.T) {
			f := newReleasedPreclockFixture(t)
			f.fullEvidence(t, func(d map[string]any) {
				if kind == "runtime_summary" {
					d["attempt-1/pre-clock-generation-1/summary.json"] = map[string]any{"schema": "wukongim.chat_lifecycle.pre_clock_summary/v1", "syntax_valid": true, "status": "parsed", "coordinator_code": "runtime", "worker_runtime_code": "engine_cpu_saturated"}
				}
				if kind == "malformed_schema" {
					d["attempt-1/receipt.json"].(map[string]any)["schema"] = "unrelated"
				}
			})
			reason := "measured_or_runtime_predecessor"
			if kind == "stop_marker" {
				b, _ := os.ReadFile(filepath.Join(f.directory, "inventory.json"))
				var inv map[string]any
				_ = json.Unmarshal(b, &inv)
				inv["artifacts"] = append(inv["artifacts"].([]any), map[string]any{"id": 203, "name": "chat-lifecycle-operator-stop-request-1", "expired": false, "workflow_run": map[string]any{"id": 105}})
				b, _ = json.Marshal(inv)
				_ = os.WriteFile(filepath.Join(f.directory, "inventory.json"), b, 0600)
				r := map[string]any{}
				for k, v := range f.api["/repos/WuKongIM/WuKongIM/actions/runs/100"].(map[string]any) {
					r[k] = v
				}
				r["id"] = 105
				r["path"] = ".github/workflows/chat-lifecycle-stop.yml"
				f.api["/repos/WuKongIM/WuKongIM/actions/runs/105"] = r
				reason = "stopped_or_stop_unverified"
			}
			if kind == "malformed_schema" {
				reason = "prior_identity_mismatch"
			}
			r, _, e := f.run(t)
			if e == nil || r["reason"] != reason {
				t.Fatalf("want %s got %v %v", reason, e, r)
			}
		})
	}
}

func TestChatLifecycleReleasedPreclockPreservesBootstrapAndAPITimePrecision(t *testing.T) {
	for _, kind := range []string{"bootstrap_mismatch", "zero_within_completed_second"} {
		t.Run(kind, func(t *testing.T) {
			f := newReleasedPreclockFixture(t)
			f.fullEvidence(t, func(d map[string]any) {
				if kind == "bootstrap_mismatch" {
					d["attempt-1/receipt.json"].(map[string]any)["receipt"].(map[string]any)["tags"].(map[string]any)["wukongim-bootstrap-access-digest"] = "sha256:" + strings.Repeat("f", 64)
				}
			})
			if kind == "zero_within_completed_second" {
				jobs := f.api["/repos/WuKongIM/WuKongIM/actions/runs/103/jobs?per_page=100&page=1"].(map[string]any)["jobs"].([]any)
				jobs[0].(map[string]any)["steps"].([]any)[0].(map[string]any)["completed_at"] = "2026-10-21T10:19:19Z"
			}
			r, _, e := f.run(t)
			if kind == "bootstrap_mismatch" {
				if e == nil || r["reason"] != "prior_identity_mismatch" {
					t.Fatalf("bootstrap mismatch accepted: %v %v", e, r)
				}
			} else if e != nil || r["status"] != "ready" {
				t.Fatalf("GitHub second precision incorrectly rejects real nanosecond zero: %v %v", e, r)
			}
		})
	}
}

func TestChatLifecycleReleasedPreclockRejectsArtifactAndChildTampering(t *testing.T) {
	for _, kind := range []string{"archive_hash", "archive_size", "zip_parent_path", "oversized_document", "child_head", "child_attempt", "ancestry", "partial_child_inventory"} {
		t.Run(kind, func(t *testing.T) {
			f := newReleasedPreclockFixture(t)
			f.fullEvidence(t, func(d map[string]any) {
				if kind == "zip_parent_path" {
					d["../escape.json"] = map[string]any{}
				}
				if kind == "oversized_document" {
					d["release-selector.json"].(map[string]any)["oversized"] = strings.Repeat("x", 2*1024*1024+1)
				}
			})
			reason := "artifact_integrity"
			switch kind {
			case "archive_hash":
				f.api["/repos/WuKongIM/WuKongIM/actions/artifacts/200"].(map[string]any)["digest"] = "sha256:" + strings.Repeat("f", 64)
			case "archive_size":
				f.api["/repos/WuKongIM/WuKongIM/actions/artifacts/200"].(map[string]any)["size_in_bytes"] = 9
			case "child_head":
				f.api["/repos/WuKongIM/WuKongIM/actions/runs/102"].(map[string]any)["head_sha"] = strings.Repeat("e", 40)
				reason = "child_producer_identity"
			case "child_attempt":
				f.api["/repos/WuKongIM/WuKongIM/actions/runs/103"].(map[string]any)["run_attempt"] = 2
				reason = "child_producer_identity"
			case "ancestry":
				f.api["/repos/WuKongIM/WuKongIM/compare/"+strings.Repeat("a", 40)+"..."+strings.Repeat("b", 40)].(map[string]any)["status"] = "diverged"
				reason = "producer_identity"
			case "partial_child_inventory":
				f.api["/repos/WuKongIM/WuKongIM/actions/runs/102/artifacts?per_page=100&page=1"].(map[string]any)["total_count"] = 101
				reason = "inventory_incomplete"
			}
			r, _, e := f.run(t)
			if e == nil || r["reason"] != reason {
				t.Fatalf("want %s got %v %v", reason, e, r)
			}
		})
	}
}

func TestChatLifecycleReleasedPreclockConsumerKeepsOriginalZeroPriorPath(t *testing.T) {
	for _, stage := range []string{"rehearsal", "formal"} {
		t.Run(stage, func(t *testing.T) {
			d := t.TempDir()
			raw, _ := os.ReadFile(filepath.Join(repoRoot(t), "scripts/testdata/chat-lifecycle-released-preclock/original.json"))
			var docs map[string]any
			_ = json.Unmarshal(raw, &docs)
			p := docs["attempt-1/run-plan.json"].(map[string]any)
			p["stage"] = stage
			b, _ := json.Marshal(p)
			file := filepath.Join(d, "run-plan.json")
			_ = os.WriteFile(file, b, 0600)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "python3", filepath.Join(repoRoot(t), "scripts/chat-lifecycle/authenticate-released-preclock.py"), "validate", "--directory", d, "--run-plan", file)
			out, err := cmd.CombinedOutput()
			var r map[string]any
			if e := json.Unmarshal(out, &r); e != nil {
				t.Fatalf("controlled receipt: %s", out)
			}
			if stage == "rehearsal" {
				if err != nil || r["status"] != "first_start" {
					t.Fatalf("legacy zero prior rejected: %v %v", err, r)
				}
			} else if err == nil || r["reason"] != "sidecar_identity" {
				t.Fatalf("wrong-stage default accepted: %v %v", err, r)
			}
		})
	}
}

func TestChatLifecycleReleasedPreclockNormalizesPublicKeyCommentAndFixesOriginalDuration(t *testing.T) {
	for _, kind := range []string{"comment_words", "changed_duration"} {
		t.Run(kind, func(t *testing.T) {
			f := newReleasedPreclockFixture(t)
			f.fullEvidence(t, func(d map[string]any) {
				if kind == "changed_duration" {
					d["attempt-1/run-plan.json"].(map[string]any)["workload_duration_seconds"] = 1
				}
			})
			if kind == "comment_words" {
				for i := len(f.env) - 1; i >= 0; i-- {
					e := f.env[i]
					if strings.HasPrefix(e, "WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY=") {
						f.env = append(f.env, e+" TEST ONLY multiword comment")
						break
					}
				}
			}
			r, _, err := f.run(t)
			if kind == "comment_words" {
				if err != nil || r["status"] != "ready" {
					t.Fatalf("normalized public key comment rejected: %v %v", err, r)
				}
			} else if err == nil || r["reason"] != "predecessor_not_original_rehearsal" {
				t.Fatalf("changed duration accepted: %v %v", err, r)
			}
		})
	}
}
