# GitHub Review Agent Implementation Plan

> **For agentic workers:** execute this plan task-by-task with test-first
> changes. Do not configure GitHub Apps, Environments, Rulesets, labels, branch
> protection, or bootstrap pull requests without separate explicit operator
> authorization.

**Goal:** Replace the current label- and plan-driven Agent PR validation
protocol with an automatic, review-only GitHub Review Agent whose dedicated
App publishes the sole required `Review Agent Verdict`.

**Architecture:** A zero-permission event Signal wakes a protected
default-branch Controller. Pure usecase logic reconciles fresh GitHub facts and
signed per-PR/scheduler state. A protected dispatched Workflow builds a bounded Context
Bundle, runs deterministic minimum checks, gives their evidence and a named
Check MCP to one ephemeral Codex reviewer, validates the complete result, then
uses separate State Writer and Review Publisher Apps to persist authority and
project it into GitHub. No scheduled scanner, compatibility path, code-writing
reviewer, or automatic merge exists.

**Tech stack:** Go 1.25, GitHub Actions YAML, GitHub REST/GraphQL APIs,
GitHub Apps, official Codex Action, JSON Schema, stdio MCP, repository Rulesets,
and Markdown operational documentation.

**Design source:**
`docs/superpowers/specs/2026-07-30-review-agent-design.md`

---

### Task 1: Establish bounded Review Agent contracts and protected policy

**Files:**

- Add: `.github/review-agent/policy.json`
- Add: `.github/review-agent/review-result.schema.json`
- Add: `.github/review-agent/state.schema.json`
- Add: `.github/review-agent/prompts/review.md`
- Add: `internal/contracts/reviewagent/FLOW.md`
- Add: `internal/contracts/reviewagent/identity.go`
- Add: `internal/contracts/reviewagent/context.go`
- Add: `internal/contracts/reviewagent/evidence.go`
- Add: `internal/contracts/reviewagent/result.go`
- Add: `internal/contracts/reviewagent/state.go`
- Add: `internal/contracts/reviewagent/*_test.go`
- Add: `scripts/review_agent_schema_test.go`

- [ ] **Step 1: Write failing strict-decoder and identity tests**

Cover:

- exact 40-character head, base, test-merge, and control SHAs;
- positive repository and pull-request identities;
- canonical intent and evidence digests;
- unknown-field, trailing-JSON, oversized-string, oversized-array, and invalid
  enum rejection;
- one result decision from `approved`, `changes_required`, or `inconclusive`;
- bounded findings, file assessments, commands, sources, and explanations;
- one immutable generation identity shared by Context, Evidence, Result, and
  State;
- no model-authored field granting GitHub publication authority.

Run:

```bash
GOWORK=off go test ./internal/contracts/reviewagent -count=1
```

Expected: RED because the package does not exist.

- [ ] **Step 2: Implement the contracts with strict canonical JSON**

Keep this package free of GitHub, filesystem, process, environment, model, and
lifecycle behavior. Critical structs and constraints need English comments.

- [ ] **Step 3: Add failing policy/schema synchronization tests**

Require:

- one policy schema version;
- `main` as the initial protected base;
- `moonshotai/kimi-k3` with `high` effort;
- immutable Codex Action/CLI identifiers;
- three active repository leases and one per PR;
- 90-minute generation timeout;
- two reconsiderations and one infrastructure retry;
- a finite per-head explanation-session and response-byte budget;
- 20 inline comments;
- separate Review App and State Writer App identities/Environments;
- no rollout, compatibility, legacy label, arbitrary command, or scheduled
  scan field;
- JSON Schemas and Go enum/limit definitions to agree.

Run:

```bash
GOWORK=off go test ./scripts \
  -run '^TestReviewAgent(Policy|Schemas)' \
  -count=1
```

Expected: RED until the policy, prompt, and schemas are complete.

- [ ] **Step 4: Set measured hard limits**

Before enabling the Workflow, benchmark representative documentation-only,
Go, Web, large refactor, and generated-bundle changes. Record conservative
limits for changed files/bytes/lines, Context Bundle bytes, model response,
context tokens, per-process CPU/memory, per-command processes,
per-address-family connections/public-network bytes in `policy.json`. Every
boundary must fail closed as `inconclusive`.

- [ ] **Step 5: Commit the contract foundation**

```bash
git add .github/review-agent internal/contracts/reviewagent \
  scripts/review_agent_schema_test.go
git commit -m "feat(review-agent): define bounded review contracts"
```

### Task 2: Implement the pure lifecycle, commands, and scheduler

**Files:**

- Add: `internal/usecase/reviewagent/FLOW.md`
- Add: `internal/usecase/reviewagent/facts.go`
- Add: `internal/usecase/reviewagent/commands.go`
- Add: `internal/usecase/reviewagent/reconcile.go`
- Add: `internal/usecase/reviewagent/scheduler.go`
- Add: `internal/usecase/reviewagent/publication.go`
- Add: `internal/usecase/reviewagent/status.go`
- Add: `internal/usecase/reviewagent/*_test.go`

- [ ] **Step 1: Drive the event matrix RED**

Use table tests for:

- opened Ready PR, Draft, ready-for-review, synchronize, reopened, and closed;
- title/body/linked-intent changes with evidence reuse;
- wrong base, merge conflict, missing test-merge SHA, incomplete facts, and
  oversize classification;
- duplicate and reordered hints;
- stale worker completion after a new generation;
- automatic first attempt, two same-head reconsiderations, retry, and cancel;
- human Review changes and control-plane owner Approval;
- exact `agent/issue-N` classification without trusting Issue Agent state.

Run:

```bash
GOWORK=off go test ./internal/usecase/reviewagent \
  -run '^TestReconcile' \
  -count=1
```

Expected: RED.

- [ ] **Step 2: Implement one deterministic reconciliation decision**

`ReconcilePullRequest` receives only fresh facts, protected policy, signed
per-PR state, signed scheduler state, and current UTC time. It performs no HTTP,
Git, shell, filesystem, or model call.

It must return exactly one bounded plan:

- no-op;
- append state only;
- enqueue;
- acquire lease and dispatch review;
- supersede/cancel;
- repair projection;
- release lease and select the next queued generation.

- [ ] **Step 3: Implement the exact command parser**

Accept only:

```text
@review-agent status
@review-agent explain <question>
@review-agent reconsider <reason>
@review-agent retry
@review-agent cancel
```

Reject quoted/code-block text, ambiguous mentions, missing reasons, edited
authority, unknown commands, and unauthorized actor roles. `reconsider` is
limited to the author or a current write-capable actor; retry/cancel require
current `write`, `maintain`, or `admin`.

- [ ] **Step 3a: Separate status, explanation, and reconsideration effects**

`status` renders signed state without a model. An explicit `explain` command
may schedule one bounded read-only explanation session. It cannot run checks
or change findings/state/
Verdict; signed state records only the interaction budget and projection.
Only `reconsider` can create another same-head decision attempt.

- [ ] **Step 4: Implement the signed three-slot scheduler model**

Test:

- FIFO order;
- at most three active leases;
- one active generation per PR;
- at most one first-time external-author lease;
- exact Actions run and generation fencing;
- idempotent acquire/release;
- stale, duplicate, or wrong-run release rejection;
- queue continuation without a Cron dependency.

- [ ] **Step 5: Implement projection and status plans**

Pure plans must render:

- the one mutable status comment;
- Review summary metadata;
- exact Check external identity and conclusion;
- projection repair versus creation;
- control-plane waiting state;
- no merge, close, dismiss, resolve, branch, or commit effect.

- [ ] **Step 6: Run the package GREEN and commit**

```bash
GOWORK=off go test ./internal/usecase/reviewagent -count=1
git add internal/usecase/reviewagent
git commit -m "feat(review-agent): add deterministic review lifecycle"
```

### Task 3: Build GitHub readers, signed state storage, and two App boundaries

**Files:**

- Add: `internal/infra/reviewagentgithub/FLOW.md`
- Add: `internal/infra/reviewagentgithub/client.go`
- Add: `internal/infra/reviewagentgithub/reader.go`
- Add: `internal/infra/reviewagentgithub/app_token.go`
- Add: `internal/infra/reviewagentgithub/state_store.go`
- Add: `internal/infra/reviewagentgithub/scheduler_store.go`
- Add: `internal/infra/reviewagentgithub/projections.go`
- Add: `internal/infra/reviewagentgithub/context_builder.go`
- Add: `internal/infra/reviewagentgithub/*_test.go`

- [ ] **Step 1: Add failing complete-read tests**

Mock GitHub REST/GraphQL and require:

- exact repository, pull request, head/base/merge, Draft, author association,
  permissions, Reviews, threads, comments, linked Issue, and Check facts;
- complete pagination whose count matches GitHub's declared changed files;
- both paths for renames;
- rejection of redirects, wrong media type, unknown fields, oversized payload,
  truncated pagination, ambiguous PR association, stale head, and default-branch
  mismatch;
- event payloads used only to identify a candidate PR hint.

- [ ] **Step 2: Add failing state-chain tests**

For `review-state/pr-N` and `review-state/scheduler`, require:

- exact configured ref and canonical path, never a caller-selected ref/path;
- complete ancestry to the expected parent;
- configured State Writer App author/login;
- GitHub verified signature;
- canonical JSON and legal usecase transition;
- expected-head GraphQL commit;
- stale head, unsigned commit, wrong App, force push, discontinuity, extra file,
  symlink, or non-state ref rejection.

- [ ] **Step 3: Implement two exact token profiles**

The token minter must accept a compile-time role, not JSON permissions.

Review App token profile:

```text
checks: write
issues: write
metadata: read
pull_requests: write
```

State Writer App token profile:

```text
contents: write
metadata: read
```

Tests must verify the returned token has exactly one selected repository and
exactly the requested profile. Private keys, JWTs, and installation tokens
must never appear in errors, outputs, `GITHUB_ENV`, or Artifacts; append and
projection commands retain installation tokens in process memory only.

- [ ] **Step 4: Implement idempotent projections**

Use stable hidden markers and Check `external_id` values. Before every write:

- re-read exact PR and generation;
- re-read authoritative state;
- verify configured Review App identity;
- verify human `REQUEST_CHANGES` and control-plane Approval facts;
- reject stale or superseded work.

The adapter may create/update the one status comment, submit one Review per
generation, create bounded inline comments, and create/update the one Check
Run. It exposes no merge, branch, commit, close, dismiss, or thread-resolution
method.

- [ ] **Step 5: Run adapter tests and commit**

```bash
GOWORK=off go test ./internal/infra/reviewagentgithub -count=1
git add internal/infra/reviewagentgithub
git commit -m "feat(review-agent): add fenced GitHub adapters"
```

### Task 4: Build complete context and deterministic minimum-check planning

**Files:**

- Add: `internal/runtime/reviewagentverify/FLOW.md`
- Add: `internal/runtime/reviewagentverify/inventory.go`
- Add: `internal/runtime/reviewagentverify/instructions.go`
- Add: `internal/runtime/reviewagentverify/policy.go`
- Add: `internal/runtime/reviewagentverify/context.go`
- Add: `internal/runtime/reviewagentverify/*_test.go`

- [ ] **Step 1: Port path risk rules with RED tests**

Re-express the existing behavior currently covered by
`agent_pr_validation_plan_test.go` as Go policy tests:

- Go/module/config/Docker/Workflow/CODEOWNERS paths;
- production shell/integration test paths;
- Web and tracked Manager bundle;
- Demo and tracked Demo bundle;
- exclusive documentation-only paths;
- rename from production into documentation;
- incomplete inventory;
- risk-selected race, integration, E2E, and three-node-cluster choices.

The result is a mandatory named-check set, not labels.

- [ ] **Step 2: Build the complete inventory**

Require exact file count, old/new rename identity, bounded patch/full-file
data, file mode/type, generated classification, and one risk-assessment slot
for every path. No truncation or sample approval is allowed.

- [ ] **Step 3: Freeze applicable instructions**

Discover every applicable `AGENTS.md` and `FLOW.md` from the exact base/control
tree, record its blob SHA and scope, and include it as trusted instruction
data. A candidate addition or modification is an untrusted control-plane diff
and cannot govern its own review. Other candidate repository content remains
untrusted relative to the protected Review Agent system prompt.

- [ ] **Step 4: Build a bounded Context Bundle**

Include exact intent, linked facts, unresolved threads, prior findings,
mandatory plans/results, inventory, instructions, and bounded repository
context. Reject oversized bundles rather than silently dropping paths.

- [ ] **Step 5: Run tests and commit**

```bash
GOWORK=off go test ./internal/runtime/reviewagentverify \
  -run '^(TestInventory|TestInstruction|TestPolicy|TestContext)' \
  -count=1
git add internal/runtime/reviewagentverify
git commit -m "feat(review-agent): build complete review context"
```

### Task 5: Add the credential-free Verifier and monitored Check MCP

**Files:**

- Add: `internal/runtime/reviewagentverify/runner.go`
- Add: `internal/runtime/reviewagentverify/baseline.go`
- Add: `internal/runtime/reviewagentverify/ledger.go`
- Add: `internal/runtime/reviewagentverify/validate.go`
- Add: `internal/runtime/reviewagentverify/*_integration_test.go`
- Add: `internal/access/reviewagentcheckmcp/FLOW.md`
- Add: `internal/access/reviewagentcheckmcp/server.go`
- Add: `internal/access/reviewagentcheckmcp/server_test.go`

- [ ] **Step 1: Drive baseline verification RED**

Cover fixed named checks, timeouts, output limits, cancellation, real exit
codes, test-merge identity, working-directory containment, and no caller
command/path/environment override.

- [ ] **Step 2: Implement the named Check MCP**

Expose only:

```text
check_list
check_run(name)
check_result(name)
```

The protected policy resolves each name to a fixed executable, argument list,
timeout, output limit, and network/process budget. The MCP writes an
append-only evidence ledger outside the read-only model session. Free-form
commands, paths, URLs, environments, and test patterns are rejected.

- [ ] **Step 3: Validate the final model result against real evidence**

Require:

- every changed path has a bounded risk assessment;
- every mandatory check has one trusted terminal result;
- every model-cited check corresponds to ledger evidence;
- an approved result has no blocking finding or failed mandatory/selected
  check;
- missing, malformed, contradictory, or stale evidence becomes
  `inconclusive`;
- post-session tracked repository content equals the immutable review input.

- [ ] **Step 4: Add integration-tagged process and network tests**

Integration tests must prove:

- public internet is available;
- RFC1918, link-local, cloud metadata, runner-host, and configured organization
  networks are unreachable;
- no `sudo` and no usable Docker socket;
- no GitHub/App/cloud/deploy/package credential reaches the process;
- process, connection, byte, and wall-time limits terminate abusive fixtures;
- the MCP ledger remains inaccessible for model writes.

Use the `integration` build tag because these tests open processes, use real
network policy, and exercise deadlines.

Run:

```bash
GOWORK=off go test ./internal/runtime/reviewagentverify \
  ./internal/access/reviewagentcheckmcp -count=1
GOWORK=off go test -tags=integration \
  ./internal/runtime/reviewagentverify \
  ./internal/access/reviewagentcheckmcp \
  -count=1
```

- [ ] **Step 5: Commit the verifier**

```bash
git add internal/runtime/reviewagentverify \
  internal/access/reviewagentcheckmcp
git commit -m "feat(review-agent): add trusted review evidence"
```

### Task 6: Add the strict CLI and standalone composition root

**Files:**

- Add: `internal/access/reviewagentcli/FLOW.md`
- Add: `internal/access/reviewagentcli/command.go`
- Add: `internal/access/reviewagentcli/command_test.go`
- Add: `internal/app/review_agent.go`
- Add: `internal/app/review_agent_test.go`
- Add: `internal/app/review_agent_internal_test.go`
- Add: `cmd/wkreviewagent/main.go`
- Add: `cmd/wkreviewagent/main_test.go`
- Modify: `internal/FLOW.md`
- Modify: `internal/app/FLOW.md`
- Modify: root `AGENTS.md` Directory Guide

- [ ] **Step 1: Define the JSON-only command surface**

Use bounded input files/stdin and one JSON result for:

```text
reconcile-github
recover-review
build-context
verify-baseline
validate-review-result
append-state
publish-review
```

Unknown commands, flags, fields, trailing input, oversized input, and missing
role configuration fail closed. Generic stderr errors must not echo input or
credentials.

- [ ] **Step 2: Compose role-specific dependencies**

`NewReviewAgentOperations` wires pure usecases to:

- read-only GitHub reader;
- Context Builder;
- credential-free Verifier;
- Review State Writer App adapter;
- Review Agent App projection adapter.

The append-state operation must not construct a Review App minter. The
publish-review operation must not construct a State Writer minter. Context and
verification commands accept no App configuration.

- [ ] **Step 3: Add composition boundary tests**

Prove that:

- no operation joins the product cluster or starts a server;
- candidate/model roles cannot reach publisher methods;
- state and Review private keys are rejected outside their exact commands;
- all GitHub writes require fresh generation fences;
- no method can merge, close, dismiss, resolve, commit code, or write a
  non-state ref.

- [ ] **Step 4: Update FLOW and directory documentation**

Record the new standalone Review Agent boundaries and exact dependency
direction. Do not describe the old validation protocol as active.

- [ ] **Step 5: Run tests and commit**

```bash
GOWORK=off go test ./cmd/wkreviewagent \
  ./internal/access/reviewagentcli \
  ./internal/access/reviewagentcheckmcp \
  ./internal/app \
  -run 'ReviewAgent|Review' \
  -count=1
git add cmd/wkreviewagent internal/access/reviewagentcli \
  internal/app/review_agent* internal/FLOW.md internal/app/FLOW.md AGENTS.md
git commit -m "feat(review-agent): compose standalone reviewer"
```

### Task 7: Add the zero-permission Signal and event-driven Controller

**Files:**

- Add: `.github/workflows/review-agent-pr-signal.yml`
- Add: `.github/workflows/review-agent.yml`
- Add: `scripts/review_agent_workflows_test.go`

- [ ] **Step 1: Write the Workflow security contracts RED**

Require the Signal to:

- use `pull_request_target` for lifecycle events;
- include formal Review events and newly created top-level command comments;
- have exact empty permissions;
- contain no `uses`, checkout, cache, artifact, secret, Environment, API call,
  candidate execution, or scheduled trigger;
- emit only an event-kind assertion and a bounded run name containing the PR
  hint.

Require the Controller to:

- wake only from the exact successful Signal Workflow or authorized
  `workflow_dispatch`;
- check out the exact current protected `main` control SHA with persisted
  credentials disabled;
- re-read all PR/state/permission facts;
- serialize state writes repository-wide;
- call no model and execute no candidate code;
- contain no Cron or open-PR scheduled scan.

Run:

```bash
GOWORK=off go test ./scripts \
  -run '^TestReviewAgent(Signal|Controller)Workflow' \
  -count=1
```

Expected: RED.

- [ ] **Step 2: Implement the Signal**

Its run completion is a hint only. Do not pass authority through artifacts,
outputs, comments, labels, or writable statuses.

- [ ] **Step 3: Implement Controller and scheduler leasing**

Jobs:

```text
read/reconcile
  -> state-writer acquire/append in review-agent-state-writer Environment
  -> status-publisher update in review-agent-publisher Environment
  -> protected review-agent-run dispatch when a lease is acquired
  -> terminal release/next-queue state append
  -> bounded self-dispatch when signed scheduler state has more work
```

Only the isolated drain job receives `actions: write`, used solely to dispatch
the exact `review-agent.yml` Workflow. It receives no App key or candidate
content.

The status-publisher receives only the Review Agent App key and projects
queued/in-progress state. It shares no job or Environment with state writes.

- [ ] **Step 4: Test event loss behavior**

There is no automatic time-based repair. Missing hints leave the Check absent
or blocked. Exact `@review-agent retry` or authorized `workflow_dispatch`
re-enters reconciliation idempotently.

- [ ] **Step 5: Parse Workflows, run contracts, and commit**

```bash
ruby -e 'require "yaml"; ARGV.each { |f| YAML.load_file(f) }' \
  .github/workflows/*.yml
GOWORK=off go test ./scripts \
  -run '^TestReviewAgent(Signal|Controller)Workflow' \
  -count=1
git add .github/workflows/review-agent-pr-signal.yml \
  .github/workflows/review-agent.yml \
  scripts/review_agent_workflows_test.go
git commit -m "feat(review-agent): add event-driven review control"
```

### Task 8: Add the dispatched Review Agent execution Workflow

**Files:**

- Add: `.github/workflows/review-agent-run.yml`
- Modify: `scripts/review_agent_workflows_test.go`

- [ ] **Step 1: Drive job-boundary contracts RED**

Require exact jobs:

```text
recover
context
baseline
review
evidence
state-writer
review-publisher
```

The contract must prove:

- candidate checkout is the exact frozen test-merge SHA;
- all checkouts use `persist-credentials: false`;
- reviewer has only the OpenAI secret and no App/GitHub/cloud/deploy secret;
- state-writer has only the State Writer App key and its Environment;
- review-publisher has only the Review Agent App key and its Environment;
- Publisher jobs never execute candidate code or download a candidate tree;
- failure paths still produce an authoritative `inconclusive` state when
  identity is recoverable;
- successful Artifacts retain 7 days and non-success evidence 30 days;
- the Workflow has no merge, push, branch, close, dismiss, or arbitrary
  repository-dispatch operation.

- [ ] **Step 2: Build trusted preparation**

Build `wkreviewagent` from the frozen protected control SHA. Prefetch pinned
dependencies and tools before candidate execution. Prepare the exact
test-merge workspace, mandatory checks, network fence, isolated home, and Check
MCP without persisted credentials.

- [ ] **Step 3: Invoke one pinned Codex reviewer**

Use the protected policy's exact:

- official Action full SHA;
- Codex CLI version;
- model and high effort;
- trusted read-only model session plus per-check disposable worktrees;
- output JSON Schema;
- system prompt and Context Bundle;
- local stdio Check MCP.

Drop `sudo`, disable Docker socket access, expose full public internet through
the tested private-network fence, and keep the model job within the 90-minute
generation deadline.

- [ ] **Step 3a: Add the bounded explanation mode**

The dispatched Workflow accepts only a protected Controller-selected
`review` or `explain` operation. `explain` skips baseline execution, the Check
MCP, decision publication, and decision-state transition. The State Writer
still records its reserved/consumed explanation budget and reply identity. It
uses the same pinned model to produce one bounded reply from the relevant
signed finding/thread context. It cannot create or change
`Review Agent Verdict`.

- [ ] **Step 4: Validate and publish**

The evidence job validates the model result and trusted ledger. The State
Writer appends authoritative state first. The Review Publisher then re-reads
fresh facts and state before repairing or publishing:

- one status comment;
- one formal Review;
- at most 20 inline comments;
- `Review Agent Verdict`.

Projection failure never rewrites an approved state to appear successful;
branch protection remains blocked until an idempotent retry repairs it.

- [ ] **Step 5: Cover terminal result mapping**

Fixtures must prove:

- `approved` -> `APPROVE` + Check `success`;
- `changes_required` -> `REQUEST_CHANGES` + Check `failure`;
- `inconclusive` -> `COMMENT` + Check `action_required`;
- control-plane approval missing -> Agent Review retained, Check waiting;
- stale generation -> no state/projection write;
- human requested changes -> preserved independently of Agent approval.

- [ ] **Step 6: Run contracts and commit**

```bash
ruby -e 'require "yaml"; ARGV.each { |f| YAML.load_file(f) }' \
  .github/workflows/*.yml
GOWORK=off go test ./scripts \
  -run '^TestReviewAgent.*Workflow' \
  -count=1
git add .github/workflows/review-agent-run.yml \
  scripts/review_agent_workflows_test.go
git commit -m "feat(review-agent): run isolated pull request reviews"
```

### Task 9: Integrate Issue Agent review remediation without shared authority

**Files:**

- Modify: `internal/usecase/issueagent/reconcile.go`
- Modify: `internal/usecase/issueagent/reconcile_test.go`
- Modify: `internal/infra/issueagentgithub/reader.go`
- Modify: `internal/infra/issueagentgithub/reader_test.go`
- Modify: `.github/workflows/issue-agent-pr-signal.yml`
- Modify: `scripts/issue_agent_workflows_test.go`
- Modify: `docs/agents/issue-agent.md`

- [ ] **Step 1: Add failing independent-review loop tests**

For exact `agent/issue-N` PRs:

- recognize Review Agent App `REQUEST_CHANGES`;
- accept only fresh unresolved blocking findings from that configured App;
- dispatch at most the existing two Issue Agent review repair iterations;
- ignore Review Agent status comments as commands;
- never trust Review Agent test claims as Issue Agent candidate evidence;
- stop at budget exhaustion and request human help.

- [ ] **Step 2: Reconcile from GitHub facts**

Keep the two Agents coupled only through normal GitHub Review/commit state. Do
not add a direct API call, shared state schema, shared credential, shared model
session, or Review Agent branch-write capability.

- [ ] **Step 3: Update Issue Agent operational documentation**

Replace the current “maintainer runs Agent Validation Gate” handoff with:

```text
Issue Agent Draft PR
  -> maintainer marks Ready
  -> independent Review Agent review
  -> bounded Issue Agent repair when changes are requested
  -> human merge
```

- [ ] **Step 4: Run Issue Agent regression tests and commit**

```bash
GOWORK=off go test ./internal/usecase/issueagent \
  ./internal/infra/issueagentgithub \
  ./scripts \
  -run 'IssueAgent|ReviewAgent' \
  -count=1
git add internal/usecase/issueagent \
  internal/infra/issueagentgithub \
  .github/workflows/issue-agent-pr-signal.yml \
  scripts/issue_agent_workflows_test.go \
  docs/agents/issue-agent.md
git commit -m "feat(issue-agent): consume independent review findings"
```

### Task 10: Replace the old validation system completely

**Files:**

- Delete: `.github/workflows/agent-pr-merge-gate.yml`
- Delete: `.github/workflows/agent-pr-validation-control.yml`
- Delete: `.github/workflows/agent-pr-validation.yml`
- Delete: `scripts/agent-pr-validation-plan.sh`
- Delete: `scripts/agent_pr_validation_plan_test.go`
- Modify: `scripts/github_workflows_test.go`
- Modify: `scripts/cloud_sim_finalize_integration_test.go`
- Modify: `scripts/cloud_sim_analyze_integration_test.go`
- Modify: `.github/CODEOWNERS`
- Modify: `.github/workflows/README.md`
- Modify: `docs/development/CI.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
- Modify: any active document or script found by the legacy-absence scan

- [ ] **Step 1: Add a failing legacy-absence contract**

Require no live code, Workflow, active test, or operational document to
contain:

```text
agent-pr-merge-gate.yml
agent-pr-validation-control.yml
agent-pr-validation.yml
agent-pr-validation-plan.sh
agent-ci/
Agent Validation Gate
agent-validation-plan:v1
```

Historical design/plan documents under `docs/superpowers` may remain as Git
history records but cannot be linked as current operation.

- [ ] **Step 2: Delete the old implementation**

Do not translate its statuses, comments, labels, retry generations, or
branch-protection context. Port only the still-valid path-to-minimum-check
behavior already covered by the new Go policy tests.

- [ ] **Step 3: Remove legacy cloud-simulation trigger assumptions**

Cloud analysis/remediation tests and scripts must wait for the automatically
created `Review Agent Verdict`; they must not add `agent-ci/run`, publish a
validation plan, or select suites.

- [ ] **Step 4: Install control-plane ownership**

Add:

```text
/.github/review-agent/ @WuKongIM/review-agent-owners
/.github/workflows/review-agent*.yml @WuKongIM/review-agent-owners
/cmd/wkreviewagent/ @WuKongIM/review-agent-owners
/internal/access/reviewagentcli/ @WuKongIM/review-agent-owners
/internal/access/reviewagentcheckmcp/ @WuKongIM/review-agent-owners
/internal/contracts/reviewagent/ @WuKongIM/review-agent-owners
/internal/usecase/reviewagent/ @WuKongIM/review-agent-owners
/internal/runtime/reviewagentverify/ @WuKongIM/review-agent-owners
/internal/infra/reviewagentgithub/ @WuKongIM/review-agent-owners
/internal/app/review_agent.go @WuKongIM/review-agent-owners
/internal/app/review_agent_internal_test.go @WuKongIM/review-agent-owners
/internal/app/review_agent_test.go @WuKongIM/review-agent-owners
/scripts/review_agent* @WuKongIM/review-agent-owners
/AGENTS.md @WuKongIM/review-agent-owners
**/AGENTS.md @WuKongIM/review-agent-owners
**/FLOW.md @WuKongIM/review-agent-owners
/.github/CODEOWNERS @WuKongIM/review-agent-owners @tangtaoit @No8blackball
```

Keep non-Review control-plane owners accurate. `CODEOWNERS` itself must remain
owned by the Review Agent owners and existing repository owners.

- [ ] **Step 5: Rewrite active CI documentation**

Document:

- automatic non-Draft `main` review;
- no scheduled scanner;
- one status comment, formal Review, and required Check;
- three decisions and exact binding;
- two App boundaries;
- human Review and emergency Ruleset bypass;
- no label/plan protocol;
- direct migration procedure.

Record the stable rule concisely in `PROJECT_KNOWLEDGE.md`.

- [ ] **Step 6: Run absence and active documentation tests**

```bash
GOWORK=off go test ./scripts \
  -run '^(TestReviewAgent|TestLegacyAgentPRValidationIsAbsent)' \
  -count=1
rg -n 'agent-ci/|Agent Validation Gate|agent-validation-plan:v1' \
  .github scripts docs/development docs/agents
```

Expected: the test passes and `rg` returns no active-protocol hit.

- [ ] **Step 7: Commit the direct replacement**

```bash
git add -A .github scripts docs/development docs/agents
git commit -m "refactor(ci): replace PR validation with Review Agent"
```

### Task 11: Add provisioning and direct-cutover runbooks

**Files:**

- Add: `docs/agents/review-agent.md`
- Add: `docs/superpowers/runbooks/review-agent-bootstrap.md`
- Modify: `.github/workflows/README.md`

- [ ] **Step 1: Document the three protected Environments**

Configure contracts for:

- `review-agent-model`: OpenAI key only;
- `review-agent-state-writer`: State Writer App key only;
- `review-agent-publisher`: Review Agent App key only.

No job may receive more than one of these credential classes.

- [ ] **Step 2: Document the two App manifests**

List exact repository permissions, login/App/installation/repository IDs,
secret names, rotation, token-response verification, and incident revocation.

- [ ] **Step 3: Document Rulesets**

Before merge:

- protect `main` and all non-state refs from the State Writer App;
- allow the State Writer App only under `review-state/**`;
- require one Code Owner Approval for Review Agent control paths;
- configure the emergency administrator bypass with audit expectations;
- retain the old required check until the migration merge completes.

- [ ] **Step 4: Document the frozen cutover**

The operator sequence is:

1. provision Apps, team, Environments, secrets, and Rulesets;
2. merge the migration PR under the old gate;
3. accept temporary merge freeze;
4. dispatch exact reconciliation for every open non-Draft `main` PR;
5. run one same-repository and one Fork bootstrap PR;
6. confirm the exact Review Agent App owns `Review Agent Verdict`;
7. atomically replace branch protection's old check with the new App-bound
   Check;
8. delete every `agent-ci/*` label;
9. verify open PRs are blocked unless their new Verdict succeeds.

No compatibility state or Shadow period is introduced.

- [ ] **Step 5: Add operator verification commands**

Include read-back commands for:

- App installations and exact permissions;
- Environment names;
- Ruleset ref patterns and bypass actors;
- CODEOWNERS enforcement;
- branch protection strictness, administrator enforcement, and exact App ID;
- open PR Check ownership;
- absence of old labels and Workflows.

- [ ] **Step 6: Commit operations documentation**

```bash
git add docs/agents/review-agent.md \
  docs/superpowers/runbooks/review-agent-bootstrap.md \
  .github/workflows/README.md
git commit -m "docs: add Review Agent operations"
```

### Task 12: Prove the complete replacement

**Files:**

- Verify: `.github/review-agent/**`
- Verify: `.github/workflows/*.yml`
- Verify: `cmd/wkreviewagent/**`
- Verify: `internal/**/reviewagent*/**`
- Verify: `scripts/**`
- Verify: active operational documentation

- [ ] **Step 1: Parse every Workflow and JSON artifact**

```bash
ruby -e 'require "yaml"; ARGV.each { |f| YAML.load_file(f) }' \
  .github/workflows/*.yml
jq -e . .github/review-agent/*.json >/dev/null
```

- [ ] **Step 2: Run focused Review Agent unit tests**

```bash
GOWORK=off go test \
  ./cmd/wkreviewagent \
  ./internal/access/reviewagentcli \
  ./internal/access/reviewagentcheckmcp \
  ./internal/contracts/reviewagent \
  ./internal/usecase/reviewagent \
  ./internal/runtime/reviewagentverify \
  ./internal/infra/reviewagentgithub \
  ./internal/app \
  ./scripts \
  -run 'ReviewAgent|LegacyAgentPRValidation' \
  -count=1
```

- [ ] **Step 3: Run the complete directly affected default unit tiers**

```bash
GOWORK=off go test \
  ./cmd/wkreviewagent \
  ./internal/access/reviewagentcli \
  ./internal/access/reviewagentcheckmcp \
  ./internal/contracts/reviewagent \
  ./internal/usecase/reviewagent \
  ./internal/runtime/reviewagentverify \
  ./internal/infra/reviewagentgithub \
  ./internal/usecase/issueagent \
  ./internal/infra/issueagentgithub \
  ./internal/app \
  ./scripts/... \
  -count=1
```

- [ ] **Step 4: Run integration-tagged isolation tests**

```bash
GOWORK=off go test -tags=integration \
  ./internal/runtime/reviewagentverify \
  ./internal/access/reviewagentcheckmcp \
  -count=1
GOWORK=off go test -tags=integration ./scripts/... \
  -count=1 -timeout=9m -parallel=2
```

- [ ] **Step 5: Execute the event/state mutation suite**

Require every negative mutation to fail:

- grant Signal permission or checkout;
- expose either App key to the reviewer;
- combine State Writer and Review Publisher jobs;
- add a scheduled trigger;
- load candidate `AGENTS.md` or `FLOW.md` as authority for its own review;
- accept an incomplete inventory;
- publish from a stale generation;
- let the State Writer target a non-state ref;
- let the Review App create a commit or merge;
- accept a same-named Check from the wrong App;
- approve with failed/missing evidence;
- exceed reconsideration, retry, queue, or comment budgets.

- [ ] **Step 6: Perform authorized bootstrap drills**

Only after explicit operator authorization:

- same-repository approved PR;
- Fork approved PR;
- seeded code defect -> `changes_required`;
- seeded provider/network failure -> `inconclusive`;
- new commit while reviewer runs -> old publisher rejected;
- control-plane change -> waits for Review Agent owner Approval;
- Issue Agent PR -> bounded review/fix/re-review loop.

- [ ] **Step 7: Verify repository state**

```bash
git diff --check origin/main...HEAD
git status --short --branch
git diff --stat origin/main...HEAD
```

Expected: no whitespace errors, no unintended files, and a final tree with
only the Review Agent path—not a transitional or compatible Agent PR
validation path.
