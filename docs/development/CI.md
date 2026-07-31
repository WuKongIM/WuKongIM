# Pull-request review and CI

WuKongIM uses a review-only Review Agent as the automated pull-request
adjudicator. The authoritative Workflow catalog lives in
[`.github/workflows/README.md`](../../.github/workflows/README.md).

## Pull-request flow

An open, non-Draft pull request targeting `main` automatically produces one
generation bound to its exact head SHA, base SHA, test-merge SHA, intent
digest, and signed-state parent.

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
`inconclusive`, stale state, missing evidence, merge conflict, infrastructure
failure, or a required control-plane owner approval all keep it blocked. A
human `REQUEST_CHANGES` Review remains independently blocking.

The Agent never edits code, commits, pushes, rebases, merges, closes a pull
request, dismisses a Review, or resolves a thread.

Draft pull requests do not call the model. A new commit or intent change
invalidates the old generation. The repository runs at most three model
sessions, one per pull request, and at most one first-time external
contributor session.

There is no Review Agent Cron or periodic scan.

## Commands

Commands must be one exact, unedited, single-line pull-request comment:

- `@review-agent status`
- `@review-agent explain <question>`
- `@review-agent reconsider <reason>`
- `@review-agent retry`
- `@review-agent cancel`

Status is deterministic and does not call a model. Explain cannot change the
verdict. Reconsider is limited to two sessions per head. Infrastructure retry
is limited to one. Retry and cancel require current write-capable repository
permission; reconsider is available to the pull-request author or a
write-capable maintainer.

Ordinary comments, including Review Agent's own status comment, are observed
no-ops.

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
working directories, and output bounds, then records a ledger outside the
read-only model session.

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
- The model receives public internet with private, link-local, metadata,
  runner-host, and configured organization CIDRs blocked. The Action's local
  proxy is the sole transport loopback exception; the permission profile still
  denies model-initiated localhost. It has no Docker socket, loses `sudo`
  before execution, and uses the distribution `bwrap` under Ubuntu's official
  path-specific AppArmor profile.
- The protected Check MCP is required at Codex startup; missing named-check
  tools fail closed instead of degrading to an evidence-free model session.
- The State Writer App can write only Contents state refs.
- The Review Agent App can write only Issues, Reviews, and Checks.
- Publisher jobs do not check out or execute candidate code.
- Durable PR and scheduler state use a verified latest-plus-predecessor rolling
  checkpoint; older App-authored commits remain append-only audit history.

Each signed lease has one 90-minute wall-time budget, including its one
automatic infrastructure retry. Reconsideration and explanation leases use the
same rule. Late review results fail closed as `inconclusive`; late explanations
cannot change the decision. Merge conflicts are deterministic
`changes_required` decisions and do not consume a model session.
Every named trusted check is capped at 30 minutes. The workflow reserves up to
30 minutes for baseline checks, 40 minutes for the reviewer, and 20 minutes for
evidence validation and signed-state publication within the same lease.

Pull-request changes to `AGENTS.md`, `FLOW.md`, policy, prompts, schemas,
Workflows, CODEOWNERS, or Review Agent code cannot govern their own review.
The protected base/control versions remain authoritative.

## Control-plane changes

A control-plane change needs both:

- an approved Review Agent decision; and
- a fresh, non-author Approval from a login in the protected
  `@WuKongIM/review-agent-owners` snapshot.

The repository Ruleset should request the same CODEOWNERS team and require only
`Review Agent Verdict` from the dedicated Review Agent App as its automated
status check. It must also require the pull-request branch to be up to date
with `main` before merge.

## Local verification

```bash
GOWORK=off go test ./internal/contracts/reviewagent \
  ./internal/usecase/reviewagent ./internal/runtime/reviewagentverify \
  ./internal/infra/reviewagentgithub ./internal/access/reviewagentcli \
  ./internal/access/reviewagentcheckmcp ./internal/app \
  ./cmd/wkreviewagent ./cmd/wkreviewcheckmcp ./scripts -count=1

GOWORK=off go test -tags=integration \
  ./internal/runtime/reviewagentverify -count=1

go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.9 \
  .github/workflows/review-agent-pr-signal.yml \
  .github/workflows/review-agent.yml \
  .github/workflows/review-agent-run.yml
```
