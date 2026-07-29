# GitHub Actions tool catalog

This directory contains two kinds of Workflow:

- `Agent Tool - ...` is an on-demand capability. An Agent may invoke test and
  read-only tools under the contracts below. Workflows that create resources,
  deploy, change permissions, or spend money require explicit user
  authorization and an approved budget.
- `Safety Automation - ...` is an autonomous safety backstop. Lease cleanup and
  live-run patrols and PR merge-gate evaluation must not depend on an Agent
  remaining online.

Use the stable filename when invoking a Workflow. Display names are optimized
for discovery and may become more specific over time.

## Catalog

| File | Display name | Trigger and purpose | Authority |
| --- | --- | --- | --- |
| `agent-pr-validation.yml` | `Agent Tool - Validate PR` | Trusted `repository_dispatch` worker that validates one exact PR head and test-merge commit, then publishes PR/gate-generation evidence | Agent may invoke through the label protocol |
| `agent-pr-validation-control.yml` | `Safety Automation - Agent PR Validation Control` | Converts an authorized `agent-ci/run` label into a trusted request and invalidates edited, opened, reopened, or synchronized PR state | Autonomous control plane; never checks out PR code |
| `agent-pr-merge-gate.yml` | `Safety Automation - Agent PR Merge Gate` | Creates a failing PR-event-bound `Agent Validation Gate`; the terminal worker reruns that same check to verify request and worker evidence | Autonomous read-only merge-gate verifier; never checks out PR code |
| `cloud-sim-provision.yml` | `Agent Tool - Provision Cloud Simulation` | Creates a leased Alibaba Cloud Simulation Run | Explicit user authorization and budget required |
| `cloud-sim-analyze.yml` | `Agent Tool - Analyze Cloud Simulation` | Opens, inspects, or closes a bounded cloud analysis session | Agent may perform read-only inspection; mutation follows the workflow input contract |
| `cloud-sim-oidc-subject.yml` | `Agent Tool - Configure Cloud Simulation OIDC Subject` | Configures and verifies the cloud OIDC subject | Explicit permission-change authorization required |
| `cloud-sim-cleanup.yml` | `Safety Automation - Reconcile Cloud Simulation Resources` | Every 15 minutes, destroys expired leases; also supports exact authorized cleanup | Autonomous billing and resource safety backstop |
| `cloud-sim-monitor.yml` | `Safety Automation - Patrol Cloud Simulation Runs` | Every 30 minutes, patrols live runs and records bounded health evidence | Autonomous read-only safety patrol |
| `issue-agent-control.yml` | `Safety Automation - Issue Agent Control` | Reconciles Issue, comment, PR, Review, validation-completion, and recovery hints against protected policy and signed Issue state | Autonomous stateless controller; capability is capped by reviewed rollout mode |
| `issue-agent-reconcile.yml` | `Safety Automation - Issue Agent Sweeper` | Hourly bounded scan for missed authorized Issue work and expired leases | Autonomous recovery dispatcher; a saturated inventory fails closed |
| `issue-agent-run.yml` | `Agent Tool - Issue Worker` | Runs one exact signed reproduction, diagnosis, or remediation task, then publishes its validated Artifact | Model Supervisor and GitHub Publisher use separate protected Environments |

## GitHub Issue Agent rollout

The checked-in Issue Agent policy currently runs in `intake` mode.
In `intake`, the trusted control planner admits only
deterministic `intake` and fresh `authorize` operations; every command,
reconciliation, PR, Review, validation-result, merge, and recovery hint becomes
`report_only` before any signed-state Publisher is selected.
`issue-agent-control.yml` and `issue-agent-reconcile.yml` share one
non-cancelling repository scheduler group, while `issue-agent-run.yml` has one
non-cancelling group per Issue. Both use the bounded maximum pending queue so
control hints and dispatched work are not replaced while waiting. Every
Issue-writing Publisher job also shares a non-cancelling
`issue-agent-publisher-<repository>-<issue>` group across the control and
Worker workflows. Its bounded maximum pending queue keeps waiting Publisher
jobs instead of replacing them. This keeps the final checkpoint re-read and
append serial without blocking an in-flight model job, so a cancellation or
new generation can publish first and fence the later stale Worker result.
Every job builds `cmd/wkissueagent` from protected `main` control source or
checks its embedded revision. Target source uses a distinct exact-SHA checkout
with persisted Git credentials disabled.

In `intake`, no model job is eligible, so no provider secret or model quota is
consumed. Publisher access to the App and checkpoint key is limited to
deterministic intake and fresh authorization writes. The scheduled scan reads
at most one page below 100 records; saturation or any invalid signed chain
blocks admission rather than assuming capacity.

Write-capable modes retain three credential boundaries:

- `issue-agent-codex` exposes `CODEX_API_KEY` only to the pinned official
  Codex Action bootstrap;
- `issue-agent-deepseek` exposes only `DEEPSEEK_API_KEY` to the selected
  DeepSeek Supervisor;
- `issue-agent-publisher` exposes repository-scoped App and checkpoint signing
  material only to Publisher/dispatcher jobs that never execute target code.

The Codex Action is pinned by full commit SHA and runs without a prompt. It
installs the pinned CLI and matching Responses proxy, writes one otherwise
empty bootstrap home, accepts only the exact `wukongim-issue-agent` App bot in
addition to normal write-authorized actors, and irreversibly drops `sudo`
before the repository-owned Worker starts. The immediately preceding step
fails if that bootstrap-home path already exists or is a dangling symlink.
Checkout, build, dependency prefetch, reproduction binaries, and digest-pinned
Docker image pull must therefore complete before the Action. `wkissueagent`
receives only
`ISSUE_AGENT_CODEX_BOOTSTRAP_HOME`; it strictly accepts the Action's
`127.0.0.1` Responses provider and creates a new empty Codex home for every
round. The API key must not appear in Worker arguments, environment dumps,
Artifacts, prompts, responses, or logs. Action or CLI upgrades require a new
full-SHA review plus the Issue Agent Workflow and model-Adapter contract tests.

The model calls only typed tools in a digest-pinned, no-network Docker sandbox.
It cannot author trusted file/evidence/usage fields. The Publisher revalidates
the sanitized Artifact, uses an expected-head GraphQL commit on
`agent/issue-<number>`, requires verified commit signing, and never merges,
closes the Bug, or writes `main`. Bulk Artifacts retain 90 days; signed
append-only Issue checkpoints are the durable state.

The validation worker classifies only an exact `agent/issue-<number>` head as
an Issue Agent PR. Its moving-`main` frozen-scenario probe runs only for that
typed branch and only when `go-e2e` is selected. Ordinary PRs keep the generic
fixed-suite protocol even when they select `go-e2e`; they are never required to
contain an Issue Agent scenario.

See `docs/agents/issue-agent.md` for setup, rollout, budgets, key rotation,
provider selection, and recovery. Every rollout promotion must pass
`scripts/issue_agent_workflows_test.go` and occur separately from the code that
introduces the capability.

## Agent PR validation protocol

The branch-protection target is the GitHub Actions check named
`Agent Validation Gate` produced by `agent-pr-merge-gate.yml`. It runs on the
PR test-merge commit and verifies evidence for the exact PR number, head SHA,
test-merge SHA, merge-gate run ID, and trusted request run. The worker's
`Agent Validation Evidence / PR #<number> / Gate #<run-id>` commit status is
evidence, not a branch-protection target. When configuring branch protection,
select the `Agent Validation Gate` check from the GitHub Actions app; never
accept a
same-named commit status from an arbitrary writer. A missing check blocks
merging, and the gate job never uses a skip condition. Its first attempt fails
closed on PR edit, open, reopen, or synchronization. After publishing terminal
evidence, the isolated worker status job reruns that same PR-bound gate; only
the rerun may pass.

The one bootstrap exception is the PR that first adds
`agent-pr-merge-gate.yml`: its initial attempt reads the complete base Git tree
and passes only when that exact Workflow file is absent from the base branch.
Once merged, the file is present on the base and every later first attempt
fails closed. Tree/API errors and truncated tree results fail closed.

For each PR head:

1. Inspect the complete diff and record the exact 40-character head SHA. The
   control Workflow resolves and freezes the corresponding 40-character
   test-merge SHA when it accepts the request.
2. Reconcile the fixed selection labels below. Do not leave a stale selection
   label from an earlier plan.
3. Publish a versioned validation-plan comment as the same GitHub actor that
   will add the trigger label.
4. Add the one-shot `agent-ci/run` label.
5. Confirm that the control Workflow published a pending
   `Agent Validation Request / PR #<number> / Gate #<run-id>` status and
   dispatched `agent-pr-validation.yml` for the exact PR, head SHA, test-merge
   SHA, gate generation, and request run, then remove `agent-ci/run`. Selection
   labels remain as audit evidence.
6. Monitor both the worker and the PR-specific `Agent Validation Gate` check to
   a terminal result. On failure, inspect bounded logs and artifacts, fix the
   code or follow the evidence-bound retry rule, and publish a final PR summary.

Editing, opening, reopening, or adding another commit triggers both safety
Workflows. The control Workflow's invalidation job shares the validation
worker's repository-wide PR-numbered concurrency group, so it cancels a running
same-PR worker and records an audit summary without publishing an unscoped
classic commit status. Wait for that invalidation job to finish before applying
`agent-ci/run`; otherwise the shared concurrency group may cancel the fresh
worker. The merge-gate check fails closed until the Agent reassesses the diff
and publishes a fresh request. Each such PR event creates a new merge-gate run
ID, which is the validation generation: evidence from an older generation
cannot consume retries or satisfy the new gate.

### Fixed selection labels

| Label | Fixed suite |
| --- | --- |
| `agent-ci/docs-only` | Verify that every changed path is allowlisted documentation; run no code test |
| `agent-ci/go-fast` | Go formatting, module metadata, Vet, and all explicit-root unit groups |
| `agent-ci/web` | Manager Web lint, tests, type check, build, and tracked bundle check |
| `agent-ci/demo` | Chat Demo tests, build, and tracked bundle check |
| `agent-ci/go-race` | All fixed Go race matrix groups |
| `agent-ci/go-integration` | All explicit `internal/...`, `pkg/...`, and `scripts/...` integration packages |
| `agent-ci/go-e2e` | Full real-process E2E inventory and bounded Cloud Medium recipient gate |
| `agent-ci/three-node-smoke` | Base three-node-cluster smoke with real `wkcli sim` traffic |
| `agent-ci/run` | One-shot request trigger; it is never a suite selection |

The remote tool accepts no arbitrary shell command, package path, test pattern,
or free-form suite name. Focused tests belong in the Agent's local workspace.
Before the first pilot, an authorized Agent creates any missing labels with the
exact names above; alternate spellings are not accepted. Request actors need
repository `write`, `maintain`, or `admin` permission.

The validator enforces these minimums:

- The fetched file inventory must exactly match the PR's `changed_files` count;
  truncated or malformed inventories fail closed.
- Renamed files are mapped using both `filename` and `previous_filename`; moving
  production or control code under an allowlisted documentation path cannot
  downgrade the required suites.
- Go, module, script, Docker, configuration, Workflow, composite-action, and
  CODEOWNERS changes require `agent-ci/go-fast`.
- Production `scripts/**/*.sh` and `scripts/**/*_integration_test.go` changes
  also require `agent-ci/go-integration`; static AWK/JQ and default-tier test
  changes do not require the heavy suite solely because of their path.
- `web/**` or its tracked Manager bundle requires `agent-ci/web`.
- `demo/chatdemo/**` or its tracked Demo bundle requires `agent-ci/demo`.
- `agent-ci/docs-only` is exclusive and accepts only the documented path
  allowlist.
- Race, integration, E2E, and three-node smoke remain risk-based Agent choices.

### Validation-plan schema

The comment's first three lines MUST be the hidden marker, one single-line JSON
object, and the closing marker shown below. Do not put a title or other preamble
before the hidden marker; human-readable explanation MAY follow it.

```markdown
<!-- agent-validation-plan:v1
{"schema_version":1,"head_sha":"0123456789abcdef0123456789abcdef01234567","risk":"medium","selected_suites":["go-fast","go-race"],"reason":"Touches concurrent runtime state","retry_of_run_id":null}
-->

## Agent validation plan

Explain the risk, selected tools, and why other heavy tools were not selected.
```

`selected_suites` must exactly match the current selection labels after the
`agent-ci/` prefix is removed. The latest matching comment must be authored by
the trigger actor and name the current PR head. `retry_of_run_id` is `null` for
the first attempt in the current gate generation.

A failed attempt may be retried once within its gate generation only when
evidence shows a Runner, network, dependency-download, or already-known flaky
failure. The retry plan names the failed Actions run in `retry_of_run_id`;
`reason` must begin with one of
`retry-evidence:runner:`, `retry-evidence:network:`,
`retry-evidence:dependency-download:`, or `retry-evidence:known-flake:` and
then explain the inspected evidence. The validator also requires that run to
be a completed `agent-pr-validation.yml` run for the exact SHA; a stranded
`pending` generation-bound evidence status is retryable only when the run
concluded as failure, cancelled, timed out, or startup failure. Assertion,
race, or behavior failures require a new commit. A second failed evidence
status for the same PR, head, test-merge, and gate generation is terminal.
After the single retry runs, that generation cannot be requested a third time,
regardless of the retry result. A later edit, reopen, or synchronize event
creates a fresh gate generation and does not inherit the old attempt count.

## Fork and permission boundary

The request control uses `pull_request_target` only as a trusted dispatcher. It
does not checkout, source, or execute PR-controlled content. Test jobs check
out the exact test-merge SHA frozen in the trusted dispatch with
`contents: read`, no Secrets, no persisted credentials, and no writable cache.
Only isolated status jobs receive `statuses: write`.
The worker verifies the exact control Workflow run ID, actor, PR display title,
gate run ID, and pending generation-bound `Agent Validation Request`; its
terminal evidence consumes that request status so it cannot be reused. The
merge-gate Workflow runs on `pull_request`, receives read-only permissions,
checks out no code, and
accepts evidence only when the control and worker run metadata match the exact
PR, head SHA, test-merge SHA from `github.sha`, gate run ID, and request ID.
The worker reruns that exact gate run ID; GitHub preserves the original SHA
when rerunning a Workflow. The gate waits for the terminal worker conclusion
within a fixed bound, and a failed handoff rewrites successful-looking evidence
to `error` so an evidence-bound network retry remains possible. Different gate
run IDs use separate concurrency groups. Before handoff and again immediately
before success, the worker/gate verify that the run ID is still the newest gate
generation for the exact PR head and test-merge; a superseded worker cannot
cancel or pass a newer generation.

The repository currently uses the `first_time_contributors` approval policy.
Adding `agent-ci/run` is the Agent's approval decision for the bounded,
read-only test worker. Before doing so, the Agent must confirm that the PR does
not weaken the control Workflow, request Secrets, add write permissions, or
introduce deployment behavior. Workflow and CODEOWNERS changes require an
explicit Agent review recorded on the pull request before `agent-ci/run` is
added. The Agent may perform and approve that review itself; no named GitHub
user or Code Owner approval is required by branch protection. CODEOWNERS
remains review-routing metadata, not a merge gate.

## Migration state

Migration completed on 2026-07-28:

1. the protocol Workflows were merged to the default branch in PR
   [#652](https://github.com/WuKongIM/WuKongIM/pull/652);
2. every fixed label was installed;
3. the same-repository pilot
   [#654](https://github.com/WuKongIM/WuKongIM/pull/654) and Fork pilot
   [#655](https://github.com/WuKongIM/WuKongIM/pull/655) both proved the
   fail-closed first attempt, exact request/evidence binding, and successful
   rerun of the original `Agent Validation Gate`;
4. `main` branch protection was atomically changed to require only
   `Agent Validation Gate` from the GitHub Actions app (`app_id: 15368`); and
5. the legacy required contexts were removed before the transitional
   Workflows were deleted.

The former `ci.yml` and `nightly.yml` test Workflows no longer exist. Pull
requests and pushes do not start tests automatically, and there is no scheduled
test suite. An Agent inspects each change and selects the fixed validation tools
above. Autonomous schedules remain only for resource and billing safety
backstops.

## Contract checks

```bash
ruby -e 'require "yaml"; ARGV.each { |f| YAML.load_file(f) }' .github/workflows/*.yml
GOWORK=off go test ./scripts \
  -run '^(TestAgentPRValidation.*|TestAgentWorkflow.*|TestLegacyAutomaticTestWorkflowsAreAbsent)$' \
  -count=1
```
