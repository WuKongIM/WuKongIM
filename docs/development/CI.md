# Pull-request review and CI

WuKongIM uses a review-only Review Agent as the automated pull-request
adjudicator. The authoritative Workflow catalog lives in
[`.github/workflows/README.md`](../../.github/workflows/README.md).

## Pull-request flow

An open, non-Draft pull request targeting `main` produces no Agent review by
default. A repository administrator starts review by posting
`@review-agent review`; the resulting generation binds the exact head SHA,
base SHA, test-merge SHA, intent digest, and signed-state parent.

The Review Agent:

1. reads the complete changed-file inventory and trusted base/control
   instructions;
2. computes mandatory checks from protected policy;
3. reviews the complete change with one pinned ephemeral model session;
4. may add bounded checks only by protected name;
5. validates real evidence independently of model claims; and
6. publishes one mutable status comment, one formal Review, up to 20 blocking
   inline comments, and `Review Agent Verdict`.

Only `approved` unlocks the automated gate. `changes_required`,
`inconclusive`, stale state, missing evidence, merge conflict, or
infrastructure failure all keep it blocked. A human `REQUEST_CHANGES` Review
remains independently blocking.

The model never edits code, commits, pushes, rebases, merges, closes a pull
request, dismisses a Review, or resolves a thread. After approval, the
protected Publisher may merge only the exact reviewed head of a repository
administrator or organization member; every other author requires a human
merge.

Draft pull requests do not call the model. A new commit or intent change
invalidates the old generation and requires another administrator command:
`review` for a new head or `reconsider` for changed same-head facts. The
repository runs at most three model sessions, one per pull request, and at most
one first-time external contributor session.

There is no Review Agent Cron or periodic scan.

## Commands

Commands must be one exact, unedited, single-line pull-request comment:

- `@review-agent review`
- `@review-agent status`
- `@review-agent explain <question>`
- `@review-agent reconsider <reason>`
- `@review-agent retry`
- `@review-agent cancel`

Status is public, deterministic, and does not call a model. Every other command
requires current repository `admin` permission. Explain cannot change the
verdict. Reconsider is limited to two sessions per head. Infrastructure retry
is limited to one.

Ordinary comments, including Review Agent's own status comment, are observed
no-ops.

Controller no-ops stop after the fresh-fact plan is recorded. They do not enter
the credentialed State Writer or Publisher Environments and do not start the
Dispatcher. Non-no-op Controller jobs reuse one exact-control,
manifest-verified binary built by reconciliation rather than compiling it once
per authority boundary.

## Trusted checks

`.github/review-agent/policy.json` maps complete path inventory to mandatory
named checks. It includes Go unit/Vet, script integration, Workflow contracts,
Manager Web, Chat Demo, documentation, race, integration, E2E, and
three-node-cluster commands.

Pure allowlisted documentation uses only documentation contracts. Every other
path receives the repository-default Go unit/Vet pair before domain-specific
checks are added, so an unfamiliar root path cannot silently escape
deterministic validation.

The model can inspect the checkout, but model-authored commands and outcomes
never count as evidence. It can request a protected check name through the
local Check MCP. The trusted runner resolves fixed arguments, timeouts,
working directories, and output bounds, then appends catalog-bound records to
the evidence ledger.

All repository-wide Go commands use `GOWORK=off` and explicit roots. Root
`./...` is forbidden because Go package discovery ignores `.gitignore`.

## Security boundaries

- The zero-permission Signal and default-branch Controller never execute
  candidate code.
- Candidate and deterministic-check jobs receive no App, GitHub write, cloud,
  deploy, package-publish, or organization-private credentials.
- Candidate checks run in per-command disposable worktrees and a rootless
  network namespace with isolated loopback for local test servers.
- The deterministic baseline disables the runner's `sudo` binary before
  candidate commands execute.
- Codex runs with full runner-user filesystem and public-network access through
  `--dangerously-bypass-approvals-and-sandbox`. It receives no GitHub/App
  credential or inherited host environment, has no Docker socket, and loses
  `sudo` before execution. Candidate checks still use the private-network and
  Bubblewrap boundary, and tracked candidate-tree mutation fails validation.
- One root-owned loopback Responses proxy is Codex's only model transport. It
  clamps every request to the protected 32,768-token output ceiling and injects
  the OpenRouter credential. The root-only credential handoff is deleted before
  the listener is published, and runner-user Codex cannot replace the proxy or
  reach an unclamped credential path.
- The protected Check MCP is required at Codex startup; missing named-check
  tools fail closed instead of degrading to an evidence-free model session.
- The State Writer App can write only Contents state refs.
- The Review Agent App can write Issues, Reviews, Checks, and the exact-head PR
  merge endpoint. GitHub requires `contents:write` for that merge; the adapter
  exposes no generic contents, branch, or commit write.
- Publisher jobs do not check out or execute candidate code.
- Durable PR and scheduler state use a verified latest-plus-predecessor rolling
  checkpoint; older App-authored commits remain append-only audit history.

Each review infrastructure attempt has one signed 90-minute wall-time budget.
The single automatic retry remains in the same generation with a fresh signed
attempt deadline, so a complete generation is bounded to 180 minutes.
Reconsideration and explanation leases retain their own signed deadline. Late
review results fail closed as `inconclusive`; late explanations cannot change
the decision. Merge conflicts are deterministic
`changes_required` decisions and do not consume a model session.
Every named trusted check is capped at 30 minutes. The workflow reserves up to
30 minutes for baseline checks, 40 minutes for the reviewer, and 20 minutes for
evidence validation and signed-state publication within each review attempt.

Pull-request changes to `AGENTS.md`, `FLOW.md`, policy, prompts, schemas,
Workflows, CODEOWNERS, or Review Agent code cannot govern their own review.
The protected base/control versions remain authoritative.

## Review gate

The signed `Review Agent Verdict` is the sole automated review gate for every
path, including Review Agent control-plane code. CODEOWNERS records maintenance
ownership but is not an additional approval gate. The repository Ruleset must
require the dedicated Review Agent App check and keep the pull-request branch
up to date with `main` before merge.

## Local verification

```bash
GOWORK=off go test ./internal/contracts/reviewagent \
  ./internal/usecase/reviewagent ./internal/runtime/reviewagentverify \
  ./internal/infra/reviewagentgithub ./internal/access/reviewagentcli \
  ./internal/access/reviewagentcheckmcp ./internal/app \
  ./cmd/wkreviewagent ./cmd/wkreviewcheckmcp ./scripts -count=1

GOWORK=off go test -tags=integration \
  ./internal/runtime/reviewagentverify -count=1

node --test .github/review-agent/responses-budget-proxy.test.mjs

go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.9 \
  .github/workflows/review-agent-pr-signal.yml \
  .github/workflows/review-agent.yml \
  .github/workflows/review-agent-run.yml
```
