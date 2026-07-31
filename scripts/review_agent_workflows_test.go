package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestReviewAgentSignalWorkflowIsAuthorityFree(t *testing.T) {
	raw := readIssueAgentFile(
		t,
		".github/workflows/review-agent-pr-signal.yml",
	)
	var document any
	require.NoError(t, yaml.Unmarshal([]byte(raw), &document))
	for _, required := range []string{
		"pull_request_target:",
		"pull_request_review:",
		"issue_comment:",
		"startsWith(github.event.comment.body, '@review-agent status')",
		"startsWith(github.event.comment.body, '@review-agent explain')",
		"startsWith(github.event.comment.body, '@review-agent reconsider')",
		"startsWith(github.event.comment.body, '@review-agent retry')",
		"startsWith(github.event.comment.body, '@review-agent cancel')",
		"permissions: {}",
	} {
		require.Contains(t, raw, required)
	}
	for _, forbidden := range []string{
		"schedule:",
		"cron:",
		"uses:",
		"secrets.",
		"actions/checkout",
		"upload-artifact",
		"download-artifact",
		"environment:",
		"gh api",
		"curl ",
		"OPENAI_API_KEY",
	} {
		require.NotContains(t, raw, forbidden)
	}
}

func TestReviewAgentIssueSignalIsBoundedAndExplicit(t *testing.T) {
	raw := readIssueAgentFile(
		t,
		".github/workflows/review-agent-issue-signal.yml",
	)
	var document any
	require.NoError(t, yaml.Unmarshal([]byte(raw), &document))
	for _, required := range []string{
		"types: [edited, closed, reopened]",
		"actions: write",
		"pull-requests: read",
		"for page in {1..10}",
		"close(s|d)?|fix(es|ed)?|resolve(s|d)?",
		"gh workflow run review-agent.yml",
	} {
		require.Contains(t, raw, required)
	}
	require.NotContains(t, raw, "schedule:")
	require.NotContains(t, raw, "cron:")
}

func TestReviewAgentCodeownersCoversItsControlPlane(t *testing.T) {
	raw := readIssueAgentFile(t, ".github/CODEOWNERS")
	for _, required := range []string{
		"/internal/app/review_agent* @WuKongIM/review-agent-owners",
		"/.github/workflows/README.md @WuKongIM/review-agent-owners",
		"/docs/agents/review-agent.md @WuKongIM/review-agent-owners",
		"/docs/development/CI.md @WuKongIM/review-agent-owners",
		"/docs/superpowers/specs/2026-07-30-review-agent-design.md @WuKongIM/review-agent-owners",
		"/docs/superpowers/plans/2026-07-30-review-agent-implementation.md @WuKongIM/review-agent-owners",
		"/docs/superpowers/runbooks/review-agent-bootstrap.md @WuKongIM/review-agent-owners",
	} {
		require.Contains(t, raw, required)
	}
}

func TestReviewAgentControllerWorkflowSeparatesAuthority(t *testing.T) {
	raw := readIssueAgentFile(t, ".github/workflows/review-agent.yml")
	var document any
	require.NoError(t, yaml.Unmarshal([]byte(raw), &document))
	require.Contains(t, raw, "- Safety Automation - Review Agent PR Signal")
	require.Contains(t, raw, "- Agent Tool - Review Pull Request")
	require.Contains(t, raw, "signal_kind=worker_failure")
	require.Contains(t, raw, "worker_attempt:$worker_attempt")
	require.Contains(t, raw, "infrastructure_attempt")
	require.Contains(t, raw, "workflow_dispatch:")
	require.Contains(
		t,
		raw,
		"github.event.workflow_run.path == '.github/workflows/review-agent-pr-signal.yml'",
	)
	require.Contains(
		t,
		raw,
		"github.event.workflow_run.path == '.github/workflows/review-agent-run.yml'",
	)
	require.Contains(t, raw, "github.event.workflow_run.conclusion == 'success'")
	require.Contains(
		t,
		raw,
		"endsWith(github.event.workflow_run.display_title, ' accepted true')",
	)
	require.Contains(t, raw, `if [[ "$path" == ".github/workflows/review-agent-run.yml" ]]`)
	require.Contains(t, raw, `test "$path" = ".github/workflows/review-agent-pr-signal.yml"`)
	require.NotContains(t, raw, "github.event.workflow_run.name")
	require.NotContains(t, raw, "jq -er .name")
	require.NotContains(t, raw, "group: review-agent-state-")
	require.Contains(t, raw, "ref: ${{ steps.control.outputs.sha }}")
	require.Contains(t, raw, "persist-credentials: false")
	require.NotContains(t, raw, "schedule:")
	require.NotContains(t, raw, "cron:")
	require.NotContains(t, raw, "openai/codex-action")
	require.NotContains(t, raw, "OPENAI_API_KEY")
	require.NotContains(t, raw, "go test")

	writer := issueAgentJobText(t, raw, "state-writer")
	require.Contains(t, writer, "environment: review-agent-state-writer")
	require.Contains(t, writer, "REVIEW_STATE_WRITER_APP_PRIVATE_KEY")
	require.Contains(t, writer, "for attempt in {1..20}")
	require.Contains(t, writer, "control-plan.next.json")
	require.Contains(t, writer, "REVIEW_AGENT_READ_TOKEN")
	require.Contains(t, writer, `state_committed=false`)
	require.Contains(t, writer, `.signal_kind = "manual"`)
	require.Less(
		t,
		strings.Index(writer, `>"$RUNNER_TEMP/state-commit.json"`),
		strings.Index(writer, `>"$RUNNER_TEMP/scheduler-commit.json"`),
		"PR state must be committed before scheduler state",
	)
	require.NotContains(t, writer, "REVIEW_AGENT_APP_PRIVATE_KEY")
	require.NotContains(t, writer, "OPENAI_API_KEY")

	publisher := issueAgentJobText(t, raw, "status-publisher")
	require.Contains(t, publisher, "environment: review-agent-publisher")
	require.Contains(t, publisher, "REVIEW_AGENT_APP_PRIVATE_KEY")
	require.NotContains(t, publisher, "REVIEW_STATE_WRITER_APP_PRIVATE_KEY")
	require.NotContains(t, publisher, "OPENAI_API_KEY")

	dispatch := issueAgentJobText(t, raw, "dispatch")
	require.Contains(t, dispatch, "actions: write")
	require.Contains(
		t,
		dispatch,
		"group: review-agent-dispatch-${{ needs.state-writer.outputs.pull_request }}",
	)
	require.Contains(t, dispatch, "cancel-in-progress: false")
	require.Contains(t, dispatch, "gh workflow run review-agent-run.yml")
	require.Contains(
		t,
		dispatch,
		`title="Review Agent PR #${PR_NUMBER} generation from lease ${LEASE_RUN_ID} attempt ${INFRASTRUCTURE_ATTEMPT}"`,
	)
	require.Contains(t, dispatch, `select(.display_title == "'"$title"'")`)
	require.Contains(
		t,
		dispatch,
		"already records the exact signed attempt",
	)
	require.Contains(
		t,
		dispatch,
		`-f "infrastructure_attempt=$INFRASTRUCTURE_ATTEMPT"`,
	)
	require.Contains(t, dispatch, "gh workflow run review-agent.yml")
	require.Contains(t, dispatch, "actions/runs/${stale_runs[0]}/cancel")
	require.NotContains(t, dispatch, "APP_PRIVATE_KEY")
	require.NotContains(t, dispatch, "actions/checkout")

	recovery := issueAgentJobText(t, raw, "controller-recovery")
	require.Contains(t, recovery, "Retry one failed Controller effect")
	require.Contains(t, recovery, "recovery_attempt=$((attempt + 1))")
	require.NotContains(t, recovery, "actions/checkout")
	require.NotContains(t, recovery, "APP_PRIVATE_KEY")
}

func TestReviewAgentRunWorkflowMaintainsRoleIsolation(t *testing.T) {
	raw := readIssueAgentFile(t, ".github/workflows/review-agent-run.yml")
	var document struct {
		RunName string `yaml:"run-name"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(raw), &document))
	require.Equal(
		t,
		"Review Agent PR #${{ inputs.pull_request }} generation from lease "+
			"${{ inputs.lease_run_id }} attempt "+
			"${{ inputs.infrastructure_attempt }}",
		document.RunName,
	)
	for _, job := range []string{
		"recover:",
		"context:",
		"baseline:",
		"review:",
		"evidence:",
		"state-writer:",
		"review-publisher:",
		"drain:",
	} {
		require.Contains(t, raw, "\n  "+job)
	}
	require.Contains(t, raw, "workflow_dispatch:")
	require.NotContains(t, raw, "workflow_call:")
	require.NotContains(t, raw, "group: review-agent-state-")
	require.Contains(t, raw, "attempt ${{ inputs.infrastructure_attempt }}")
	require.Contains(t, raw, "INPUT_INFRASTRUCTURE_ATTEMPT")
	require.Contains(
		t,
		raw,
		".next_state.budget.infrastructure_retries_used",
	)
	require.NotContains(t, raw, "schedule:")
	require.NotContains(t, raw, "cron:")
	require.Equal(t, strings.Count(raw, "actions/checkout@"),
		strings.Count(raw, "persist-credentials: false"))
	require.Contains(t, raw, "ref: ${{ needs.recover.outputs.test_merge_sha }}")
	require.Equal(t, 1, strings.Count(raw, "openai/codex-action@"))
	require.Contains(t, raw, "--model moonshotai/kimi-k3")
	require.Contains(t, raw, `--config 'model_reasoning_effort="high"'`)
	require.Contains(t, raw, "codex-version: 0.146.0")
	require.Contains(t, raw, "codex-responses-api-proxy")
	require.Contains(t, raw, "env -u PROXY_API_KEY")
	require.Contains(t, raw, "--upstream-url https://openrouter.ai/api/v1/responses")
	require.Contains(t, raw, "--cpu=2400:2400")
	require.Contains(t, raw, "--as=8589934592:8589934592")
	require.Contains(t, raw, "--nproc=512:512")
	require.Contains(t, raw, "--dangerously-bypass-approvals-and-sandbox")
	require.NotContains(t, raw, "default_permissions")
	require.Contains(t, raw, "review_reason:$recovery[0].next_state.reason")
	require.Contains(t, raw, "retention-days: 7")
	require.Contains(t, raw, "retention-days: 30")
	require.NotContains(t, raw, "timeout-minutes: 90")
	require.Contains(t, raw, `timeout-minutes: 35`)
	require.Contains(
		t,
		raw,
		`remaining="$(( DEADLINE_EPOCH - $(date -u +%s) - 3600 ))"`,
	)
	require.Contains(t, raw, `if (( remaining < 3600 )); then`)
	require.Contains(
		t,
		raw,
		`if (( DEADLINE_EPOCH - $(date -u +%s) < 1200 )); then`,
	)
	require.Contains(
		t,
		raw,
		`"$RUNNER_TEMP/review-agent-network-fence.sh" baseline-host`,
	)
	require.NotContains(t, raw, `network-fence.sh" host disable-sudo`)
	require.Contains(
		t,
		raw,
		`"$RUNNER_TEMP/review-agent-network-fence.sh" review-host`,
	)
	require.NotContains(t, raw, `"$RUNNER_TEMP/review-agent-network-fence.sh" model-host`)
	require.Contains(t, raw, `"$RUNNER_TEMP/review-agent-network-fence.sh" join`)
	require.Contains(t, raw, `'command = "/usr/bin/env"'`)
	require.Contains(
		t,
		raw,
		`\"REVIEW_NETWORK_FENCE=$RUNNER_TEMP/review-agent-network-fence.sh\"`,
	)
	require.Contains(
		t,
		raw,
		`\"REVIEW_NETNS_PID_FILE=$RUNNER_TEMP/review-agent-netns.pid\"`,
	)
	require.NotContains(
		t,
		raw,
		`"command = \"$RUNNER_TEMP/review-agent-network-fence.sh\""`,
	)
	require.Contains(
		t,
		raw,
		`sudo chown root:root \`+"\n"+
			`              "$RUNNER_TEMP/review-agent-netns.pid"`,
	)
	require.Contains(t, raw, "model_context_window=240000")
	require.Contains(t, raw, "model_auto_compact_token_limit=216000")
	require.NotContains(t, raw, `[[ "${{ inputs.`)
	require.NotContains(t, raw, `--argjson pull_request "${{ inputs.`)
	require.Contains(t, raw, "retry_and_dispatch")
	require.Contains(t, raw, "for attempt in {1..20}")
	require.Contains(t, raw, "terminal-request.json")
	require.Contains(t, raw, "prior_finding_dispositions")
	require.Contains(t, raw, "gh workflow run review-agent-run.yml")
	require.Contains(t, raw, `-f "infrastructure_attempt=$attempt"`)
	require.Contains(t, raw, "review-agent-trusted-baseline")
	require.Contains(t, raw, "$trusted[0].checks[]")
	baseline := issueAgentJobText(t, raw, "baseline")
	require.Contains(
		t,
		baseline,
		"REVIEW_EVIDENCE_LEDGER: ${{ runner.temp }}/"+
			"review-agent-baseline-artifact/ledger.jsonl",
	)
	require.Contains(
		t,
		baseline,
		`>"$RUNNER_TEMP/review-agent-baseline-artifact/baseline-evidence.json"`,
	)
	require.Contains(
		t,
		baseline,
		"path: ${{ runner.temp }}/review-agent-baseline-artifact/",
	)

	fence := readIssueAgentFile(t, ".github/review-agent/network-fence.sh")
	require.NotContains(t, fence, "prefix=(sudo)")
	require.NotContains(t, fence, "apply_network_rules model")
	require.NotContains(t, fence, "limit_runner_worker")
	require.NotContains(t, fence, "sudo prlimit")
	require.Contains(t, fence, `ip6tables -A OUTPUT`)
	require.Contains(
		t,
		fence,
		`review_unshare_binary="$review_unshare_directory/unshare"`,
	)
	require.Contains(t, fence, `flags=(unconfined)`)
	require.Contains(t, fence, `  userns,`)
	require.Contains(t, fence, `sudo apparmor_parser -r`)
	require.Contains(t, fence, `sudo apparmor_parser -R`)
	require.Contains(t, fence, `sudo rm -f "$review_unshare_profile"`)
	require.Contains(t, fence, `sudo rm -f "$review_unshare_binary"`)
	require.Contains(t, fence, `sudo rmdir "$review_unshare_directory"`)
	require.Equal(
		t,
		3,
		strings.Count(
			fence,
			`/proc/sys/kernel/apparmor_restrict_unprivileged_userns`,
		),
		"the global userns restriction must bracket the narrow namespace exception",
	)
	require.Contains(
		t,
		fence,
		`"$review_unshare_binary" --user --map-root-user --net`,
	)
	require.Contains(
		t,
		fence,
		"trap cleanup_user_namespace_exception EXIT\n"+
			"    prepare_user_namespace\n"+
			"    start_namespace \"$2\"\n"+
			"    release_user_namespace_exception\n"+
			"    trap - EXIT",
		"start must prepare, use, and revoke its AppArmor exception atomically",
	)
	require.NotContains(t, fence, "prepare-userns")
	require.Contains(t, fence, "slirp4netns --configure --disable-host-loopback")
	require.Contains(t, fence, "baseline-host)")
	require.Contains(t, fence, "review-host)")
	require.NotContains(t, fence, "model-host)")
	require.NotContains(t, fence, "apply_network_rules host")
	require.NotContains(t, fence, "keep-sudo")
	require.NotContains(t, fence, "limit_runner_worker")
	require.NotContains(t, fence, "prepare_model_sandbox")
	require.NotContains(t, fence, "bwrap-userns-restrict")
	require.Equal(
		t,
		4,
		strings.Count(fence, "nsenter --preserve-credentials"),
		"every trusted namespace entry must retain the mapped runner credentials",
	)
	require.Contains(
		t,
		fence,
		`sed -n '1,40p' "$RUNNER_TEMP/review-agent-slirp.log" >&2`,
		"namespace startup failures must retain bounded slirp evidence",
	)
	require.Contains(
		t,
		fence,
		`nsenter --preserve-credentials -t "$REVIEW_NETNS_PID" -U -m -n \`+"\n"+
			`    ip link show >&2 || true`,
		"namespace startup failures must retain the final bounded nsenter error",
	)
	require.Contains(t, fence, "nsenter")
	require.Contains(t, fence, "--connlimit-above 128")
	require.Equal(t, 4, strings.Count(fence, "--quota 1073741824"))
	require.Contains(t, fence, "ulimit -u 512")
	require.Contains(t, fence, "ulimit -t 3600")
	require.Contains(t, fence, "ulimit -v 8388608")
	require.Contains(t, fence, `sudo chmod 000 "$sudo_binary"`)
	require.Equal(
		t,
		1,
		strings.Count(raw, "baseline-host"),
		"the production baseline must apply the host privilege fence exactly once",
	)
	require.NotContains(
		t,
		raw,
		`"$RUNNER_TEMP/review-agent-network-fence.sh" prepare-userns`,
	)
	review := issueAgentJobText(t, raw, "review")
	require.Contains(
		t,
		review,
		"if [[ \"$INPUT_OPERATION\" == review ]]; then\n"+
			"            \"$RUNNER_TEMP/review-agent-network-fence.sh\" start",
		"explanation sessions must not create a candidate network namespace",
	)
	terminalStateWriter := issueAgentJobText(t, raw, "state-writer")
	require.Contains(
		t,
		terminalStateWriter,
		"always() && needs.evidence.result == 'success'",
		"validated fail-closed evidence must reach signed state after an upstream failure",
	)
	terminalPublisher := issueAgentJobText(t, raw, "review-publisher")
	require.Contains(
		t,
		terminalPublisher,
		"always() && needs.state-writer.result == 'success'",
		"terminal Review and Verdict publication must survive an upstream failure",
	)
	terminalDrain := issueAgentJobText(t, raw, "drain")
	require.Contains(
		t,
		terminalDrain,
		"always() && needs.state-writer.result == 'success'",
		"the signed queue must drain after fail-closed completion",
	)
	require.Contains(
		t,
		raw,
		"steps.finalize.outputs.retention == 'short'",
	)
	require.Contains(
		t,
		raw,
		"steps.finalize.outputs.retention == 'long'",
	)
	evidence := issueAgentJobText(t, raw, "evidence")
	require.Contains(
		t,
		evidence,
		"name: Download trusted baseline\n"+
			"        if: inputs.operation == 'review'\n"+
			"        continue-on-error: true",
	)
	require.Equal(
		t,
		3,
		strings.Count(evidence, "continue-on-error: true"),
		"missing context, reviewer, and baseline artifacts must fail closed",
	)
	require.Contains(
		t,
		evidence,
		`"$RUNNER_TEMP/wkreviewagent" normalize-review-result`,
	)
	require.Equal(
		t,
		1,
		strings.Count(evidence, `jq -e . "$result"`),
		"only the distinct explanation contract remains strict JSON-only",
	)

	reviewer := issueAgentJobText(t, raw, "review")
	require.Contains(t, reviewer, "timeout-minutes: 40")
	require.Contains(t, reviewer, "environment: review-agent-model")
	require.Contains(t, reviewer, "secrets.OPENAI_API_KEY")
	require.Contains(t, reviewer, "--dangerously-bypass-approvals-and-sandbox")
	require.NotContains(t, reviewer, "default_permissions")
	require.Contains(t, reviewer, "allow-bots: true")
	require.Contains(t, reviewer, "required = true")
	require.Contains(t, reviewer, `default_tools_approval_mode = "approve"`)
	require.Contains(t, reviewer, `enabled_tools = ["check_result", "check_run"]`)
	require.Contains(t, reviewer, "<trusted-output-schema>")
	require.Contains(
		t,
		reviewer,
		`--cd "$RUNNER_TEMP/review-agent-session"`,
	)
	require.NotContains(t, reviewer, `PATH="/opt/wukongim-review-agent:$PATH"`)
	require.Contains(t, reviewer, "set -euo pipefail\n          prlimit \\")
	require.NotContains(t, reviewer, "[permissions.review-agent]")
	require.Contains(
		t,
		reviewer,
		`REVIEW_EVIDENCE_LEDGER=$RUNNER_TEMP/review-agent-result-artifact/ledger.jsonl`,
	)
	require.Contains(
		t,
		reviewer,
		`"$RUNNER_TEMP/review-agent-result-artifact/review-agent-output.json"`,
	)
	require.Equal(
		t,
		2,
		strings.Count(
			reviewer,
			"path: ${{ runner.temp }}/review-agent-result-artifact/",
		),
	)
	require.NotContains(t, reviewer, "sandbox: workspace-write")
	require.NotContains(t, reviewer, "REVIEW_AGENT_APP_PRIVATE_KEY")
	require.NotContains(t, reviewer, "REVIEW_STATE_WRITER_APP_PRIVATE_KEY")
	require.NotContains(t, reviewer, "git push")
	require.NotContains(t, reviewer, "gh api")

	writer := issueAgentJobText(t, raw, "state-writer")
	require.Contains(t, writer, "environment: review-agent-state-writer")
	require.Contains(t, writer, "REVIEW_STATE_WRITER_APP_PRIVATE_KEY")
	require.NotContains(t, writer, "REVIEW_AGENT_APP_PRIVATE_KEY")
	require.NotContains(t, writer, "OPENAI_API_KEY")
	require.NotContains(t, writer, "test_merge_sha")

	publisher := issueAgentJobText(t, raw, "review-publisher")
	require.Contains(t, publisher, "environment: review-agent-publisher")
	require.Contains(t, publisher, "REVIEW_AGENT_APP_PRIVATE_KEY")
	require.NotContains(t, publisher, "REVIEW_STATE_WRITER_APP_PRIVATE_KEY")
	require.NotContains(t, publisher, "OPENAI_API_KEY")
	require.NotContains(t, publisher, "test_merge_sha")
	require.NotContains(t, publisher, "go test")

	drain := issueAgentJobText(t, raw, "drain")
	require.Contains(t, drain, "actions: write")
	require.Contains(
		t,
		drain,
		"group: review-agent-drain-${{ inputs.pull_request }}",
	)
	require.Contains(t, drain, "cancel-in-progress: false")
	require.Contains(t, drain, "gh workflow run review-agent-run.yml")
	require.Contains(
		t,
		drain,
		`title="Review Agent PR #${INPUT_PULL_REQUEST} generation from lease ${lease} attempt ${attempt}"`,
	)
	require.Contains(t, drain, "already records the exact signed retry")
	require.NotContains(t, drain, "APP_PRIVATE_KEY")
	require.NotContains(t, drain, "actions/checkout")
}

func TestLegacyAgentPRValidationIsAbsent(t *testing.T) {
	root := repoRoot(t)
	for _, relative := range []string{
		".github/workflows/agent-pr-merge-gate.yml",
		".github/workflows/agent-pr-validation-control.yml",
		".github/workflows/agent-pr-validation.yml",
		"scripts/agent-pr-validation-plan.sh",
		"scripts/agent_pr_validation_plan_test.go",
	} {
		_, err := os.Stat(filepath.Join(root, relative))
		require.ErrorIs(t, err, os.ErrNotExist, relative)
	}
	for _, relative := range []string{
		".github",
		"scripts",
		"docs/development",
		"docs/agents",
	} {
		err := filepath.WalkDir(
			filepath.Join(root, relative),
			func(path string, entry os.DirEntry, walkErr error) error {
				require.NoError(t, walkErr)
				if entry.IsDir() {
					return nil
				}
				body, readErr := os.ReadFile(path)
				require.NoError(t, readErr)
				for _, legacy := range []string{
					"agent-" + "ci/",
					"Agent Validation " + "Gate",
					"agent-validation-" + "plan:v1",
				} {
					require.NotContains(t, string(body), legacy, path)
				}
				return nil
			},
		)
		require.NoError(t, err)
	}
}
