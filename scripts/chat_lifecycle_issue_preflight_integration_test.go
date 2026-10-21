//go:build integration

package scripts_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatLifecycleIssuePreflightReadOnlyIdentity(t *testing.T) {
	for _, inheritedWriteSettings := range []bool{false, true} {
		name := "minimal_environment"
		if inheritedWriteSettings {
			name = "ignores_inherited_comment_and_close"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newIssuePreflightFixture(t)
			if inheritedWriteSettings {
				fixture.env = append(fixture.env, "WK_CHAT_ISSUE_STATE=request_complete", "WK_CHAT_ISSUE_BODY=must not post", "WK_CHAT_ISSUE_CLOSE=true")
			}
			receipt, calls, err := fixture.run(t, "--preflight")
			if err != nil {
				t.Fatalf("preflight: %v; receipt=%v", err, receipt)
			}
			if receipt["status"] != "ready" || receipt["reason"] != "issue_ready" || receipt["issue_number"] != float64(42) || receipt["issue_url"] != "https://github.com/WuKongIM/WuKongIM/issues/42" {
				t.Fatalf("wrong ready identity: %v", receipt)
			}
			if len(calls) != 2 || !strings.Contains(calls[0], `/search/issues -f q=repo:WuKongIM/WuKongIM is:issue in:title "[chat-lifecycle] request-1" -f per_page=10`) || !strings.HasSuffix(calls[1], "/repos/WuKongIM/WuKongIM/issues/42") {
				t.Fatalf("want exact bounded search and Issue GET: %q", calls)
			}
		})
	}
}

func TestChatLifecycleIssuePreflightRejectsUnusableTrackingIssue(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		reads  int
		change func(*issuePreflightFixture)
	}{
		{"missing", "issue_missing", 1, func(f *issuePreflightFixture) { f.search = issuePreflightSearch() }},
		{"fuzzy_title", "issue_missing", 1, func(f *issuePreflightFixture) {
			v := issuePreflightIssue()
			v["title"] = "[chat-lifecycle] request-10"
			f.search = issuePreflightSearch(v)
		}},
		{"pull_request", "issue_missing", 1, func(f *issuePreflightFixture) {
			v := issuePreflightIssue()
			v["pull_request"] = map[string]any{}
			f.search = issuePreflightSearch(v)
		}},
		{"duplicate_including_closed", "issue_ambiguous", 1, func(f *issuePreflightFixture) {
			v := issuePreflightIssue()
			v["state"] = "closed"
			v["number"] = 43
			f.search = issuePreflightSearch(issuePreflightIssue(), v)
		}},
		{"incomplete_search", "search_incomplete", 1, func(f *issuePreflightFixture) { f.search["incomplete_results"] = true }},
		{"truncated_search", "search_incomplete", 1, func(f *issuePreflightFixture) { f.search["total_count"] = 11 }},
		{"missing_completeness", "search_invalid", 1, func(f *issuePreflightFixture) { delete(f.search, "incomplete_results") }},
		{"invalid_count", "search_invalid", 1, func(f *issuePreflightFixture) { f.search["total_count"] = "1" }},
		{"invalid_items", "search_invalid", 1, func(f *issuePreflightFixture) { f.search["items"] = nil }},
		{"malformed_json", "search_invalid", 1, func(f *issuePreflightFixture) { f.rawSearch = "not json" }},
		{"search_api_failure", "search_failed", 1, func(f *issuePreflightFixture) { f.env = append(f.env, "WK_TEST_SEARCH_FAIL=true") }},
		{"invalid_number", "issue_invalid", 1, func(f *issuePreflightFixture) {
			v := issuePreflightIssue()
			v["number"] = "42"
			f.search = issuePreflightSearch(v)
		}},
		{"missing_id", "issue_invalid", 1, func(f *issuePreflightFixture) {
			v := issuePreflightIssue()
			delete(v, "id")
			f.search = issuePreflightSearch(v)
		}},
		{"foreign_repository", "issue_invalid", 1, func(f *issuePreflightFixture) {
			v := issuePreflightIssue()
			v["repository_url"] = "https://api.github.com/repos/Other/Other"
			f.search = issuePreflightSearch(v)
		}},
		{"wrong_html_url", "issue_invalid", 1, func(f *issuePreflightFixture) {
			v := issuePreflightIssue()
			v["html_url"] = "https://github.com/Other/Other/issues/42"
			f.search = issuePreflightSearch(v)
		}},
		{"get_api_failure", "issue_read_failed", 2, func(f *issuePreflightFixture) { f.env = append(f.env, "WK_TEST_GET_FAIL=true") }},
		{"closed_after_search", "issue_closed", 2, func(f *issuePreflightFixture) { f.issue["state"] = "closed" }},
		{"locked_issue", "issue_locked", 2, func(f *issuePreflightFixture) { f.issue["locked"] = true }},
		{"get_identity_changed", "issue_invalid", 2, func(f *issuePreflightFixture) { f.issue["id"] = 101 }},
		{"get_title_changed", "issue_invalid", 2, func(f *issuePreflightFixture) { f.issue["title"] = "different request" }},
		{"get_repository_changed", "issue_invalid", 2, func(f *issuePreflightFixture) { f.issue["repository_url"] = "https://api.github.com/repos/Other/Other" }},
		{"get_pull_request", "issue_invalid", 2, func(f *issuePreflightFixture) { f.issue["pull_request"] = map[string]any{} }},
		{"get_state_missing", "issue_invalid", 2, func(f *issuePreflightFixture) { delete(f.issue, "state") }},
		{"get_comments_missing", "issue_invalid", 2, func(f *issuePreflightFixture) { delete(f.issue, "comments") }},
		{"get_comments_invalid", "issue_invalid", 2, func(f *issuePreflightFixture) { f.issue["comments"] = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newIssuePreflightFixture(t)
			tt.change(fixture)
			receipt, calls, err := fixture.run(t, "--preflight")
			if err == nil || receipt["status"] != "rejected" || receipt["reason"] != tt.reason || receipt["issue_number"] != nil || receipt["issue_url"] != nil {
				t.Fatalf("want typed %s rejection; err=%v receipt=%v", tt.reason, err, receipt)
			}
			if len(calls) != tt.reads {
				t.Fatalf("want %d reads, got %q", tt.reads, calls)
			}
		})
	}
}

func TestChatLifecycleIssuePreflightRejectsInvalidEnvironmentBeforeAPI(t *testing.T) {
	for _, setting := range []string{"GH_TOKEN=", "GITHUB_REPOSITORY=", "GITHUB_REPOSITORY=../other", "WK_CHAT_REQUEST_ID=", "WK_CHAT_REQUEST_ID=bad request"} {
		t.Run(strings.SplitN(setting, "=", 2)[0]+"/"+setting, func(t *testing.T) {
			fixture := newIssuePreflightFixture(t)
			fixture.env = append(fixture.env, setting)
			output, calls, err := fixture.execute(t, "--preflight")
			var receipt map[string]any
			if decodeErr := json.Unmarshal([]byte(output), &receipt); decodeErr != nil {
				t.Fatalf("environment rejection is not JSON: %v; %s", decodeErr, output)
			}
			if err == nil || len(calls) != 0 || receipt["status"] != "rejected" || receipt["reason"] != "environment_invalid" {
				t.Fatalf("environment must fail before API: err=%v calls=%q receipt=%v", err, calls, receipt)
			}
		})
	}
}

func TestChatLifecycleIssuePreflightRejectsUnknownFlagsBeforeAPI(t *testing.T) {
	for _, args := range [][]string{{"--typo"}, {"--preflight", "--typo"}, {"--preflight", "--preflight"}} {
		fixture := newIssuePreflightFixture(t)
		fixture.env = append(fixture.env, "WK_CHAT_ISSUE_STATE=formal_running")
		output, calls, err := fixture.execute(t, args...)
		if err == nil || !strings.Contains(output, "usage:") || len(calls) != 0 {
			t.Fatalf("flags %q must fail before API; err=%v calls=%q output=%s", args, err, calls, output)
		}
	}
}

type issuePreflightFixture struct {
	directory string
	env       []string
	search    map[string]any
	issue     map[string]any
	rawSearch string
}

func issuePreflightIssue() map[string]any {
	return map[string]any{"id": 100, "number": 42, "title": "[chat-lifecycle] request-1", "state": "open", "comments": 0, "locked": false,
		"url": "https://api.github.com/repos/WuKongIM/WuKongIM/issues/42", "repository_url": "https://api.github.com/repos/WuKongIM/WuKongIM", "html_url": "https://github.com/WuKongIM/WuKongIM/issues/42"}
}

func issuePreflightSearch(items ...map[string]any) map[string]any {
	if items == nil {
		items = []map[string]any{}
	}
	return map[string]any{"total_count": len(items), "incomplete_results": false, "items": items}
}

func newIssuePreflightFixture(t *testing.T) *issuePreflightFixture {
	t.Helper()
	directory := t.TempDir()
	stub := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$WK_TEST_CALLS"
[[ "${1:-}" == api && "${2:-}" == --hostname && "${3:-}" == github.com && "${4:-}" == --method && "${5:-}" == GET ]] || exit 91
case "${6:-}" in
  /search/issues) [[ "${WK_TEST_SEARCH_FAIL:-false}" != true ]] || exit 92; cat "$WK_TEST_SEARCH" ;;
  /repos/WuKongIM/WuKongIM/issues/42) [[ "${WK_TEST_GET_FAIL:-false}" != true ]] || exit 93; cat "$WK_TEST_ISSUE" ;;
  *) exit 94 ;;
esac
`
	if err := os.WriteFile(filepath.Join(directory, "gh"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GH_") && !strings.HasPrefix(entry, "WK_CHAT_") && !strings.HasPrefix(entry, "WK_TEST_") && !strings.HasPrefix(entry, "GITHUB_REPOSITORY=") && !strings.HasPrefix(entry, "PATH=") {
			env = append(env, entry)
		}
	}
	env = append(env, "PATH="+directory+string(os.PathListSeparator)+os.Getenv("PATH"), "GH_TOKEN=test-only-token", "GITHUB_REPOSITORY=WuKongIM/WuKongIM", "WK_CHAT_REQUEST_ID=request-1",
		"WK_TEST_CALLS="+filepath.Join(directory, "calls"), "WK_TEST_SEARCH="+filepath.Join(directory, "search.json"), "WK_TEST_ISSUE="+filepath.Join(directory, "issue.json"))
	return &issuePreflightFixture{directory: directory, env: env, search: issuePreflightSearch(issuePreflightIssue()), issue: issuePreflightIssue()}
}

func (f *issuePreflightFixture) execute(t *testing.T, args ...string) (string, []string, error) {
	t.Helper()
	for name, value := range map[string]any{"search.json": f.search, "issue.json": f.issue} {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if name == "search.json" && f.rawSearch != "" {
			body = []byte(f.rawSearch)
		}
		if err := os.WriteFile(filepath.Join(f.directory, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("bash", append([]string{filepath.Join(repoRoot(t), "scripts", "chat-lifecycle", "comment-request-issue.sh")}, args...)...)
	command.Env = f.env
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	err := command.Run()
	body, readErr := os.ReadFile(filepath.Join(f.directory, "calls"))
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	calls := []string{}
	if len(body) > 0 {
		calls = strings.Split(strings.TrimSpace(string(body)), "\n")
	}
	return output.String(), calls, err
}

func (f *issuePreflightFixture) run(t *testing.T, args ...string) (map[string]any, []string, error) {
	t.Helper()
	output, calls, err := f.execute(t, args...)
	for _, call := range calls {
		if !strings.HasPrefix(call, "api --hostname github.com --method GET ") {
			t.Fatalf("preflight attempted a non-read-only or non-GitHub command: %q", calls)
		}
	}
	var receipt map[string]any
	if decodeErr := json.Unmarshal([]byte(output), &receipt); decodeErr != nil {
		t.Fatalf("preflight did not return one JSON identity: %v; err=%v; output=%s", decodeErr, err, output)
	}
	if receipt["schema"] != "wukongim.chat_lifecycle.issue_preflight/v1" || receipt["repository"] != "WuKongIM/WuKongIM" || receipt["request_id"] != "request-1" || strings.Contains(output, "test-only-token") {
		t.Fatalf("invalid or sensitive receipt: %s", output)
	}
	return receipt, calls, err
}
