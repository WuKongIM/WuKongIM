# GitHub Review Agent

The Review Agent is a senior reviewer embedded in every ready pull request
targeting `main`. It reviews and adjudicates; it never changes code or merges.

## Lifecycle

```text
PR event
  -> Review Agent PR Signal (zero permission)
  -> Review Agent Controller (fresh GitHub facts)
  -> signed per-PR state + signed repository scheduler
  -> Context Builder + deterministic baseline checks
  -> ephemeral Review Agent model + protected named Check MCP
  -> trusted evidence validator
  -> State Writer App
  -> Review Agent App projections
```

The Signal uses `pull_request_target` only to ensure Fork events can wake the
default-branch Controller without contributor approval. It does not checkout,
execute, upload, download, call the network, or receive a token or Secret.
Controller authority comes entirely from fresh API reads and signed state.

The eligible set is every open, non-Draft pull request whose base is `main`.
Draft, closed, wrong-base, stale, conflicting, oversized, or incomplete inputs
are handled without trusting a model. No Cron or periodic pull-request scan
exists.

## Generations and state

One generation binds:

- repository and pull-request identity;
- exact head, base, and test-merge SHAs;
- canonical pull-request intent digest;
- monotonically increasing generation number; and
- exact signed-state parent revision.

Per-PR state is canonical JSON on `review-state/pr-<number>` at
`.review-agent-state/pr-<number>.json`. Repository scheduling state is
canonical JSON on `review-state/scheduler` at
`.review-agent-state/scheduler.json`.

Only canonical rolling checkpoints in GitHub-verified commits authored by the
configured Review State Writer App are authority. The hot path verifies the
latest checkpoint and its immediate predecessor; older commits remain
append-only audit history. Event payloads, branches, comments, model output,
Artifacts, status comments, Reviews, and Checks are projections or hints.

The scheduler permits three repository-wide sessions, one per pull request,
and one first-time external contributor session. Fresh-read expected-head CAS
transactions retry bounded contention while preserving every event; an
isolated `actions: write` dispatcher starts the exact worker after the state
commit. Dispatch is serialized per pull request, and the exact worker title
derived from pull request, signed lease, and infrastructure attempt is its
idempotency key at both initial and retry boundaries. Terminal work wakes the
next signed queue entry. Partial state writes and dispatches are recoverable
from signed checkpoints and leases.

## Review inputs

The trusted Context Builder includes:

- the complete paginated file inventory, exact base/head text, full-file
  review content, and GitHub's real diff hunks for inline coordinates;
- pull-request title, body, linked Issue identities, and intent digest;
- exact trusted control/base `AGENTS.md` and applicable `FLOW.md` blobs;
- current Reviews, comments, unresolved threads, and Check facts;
- protected policy, prompt, and schema digests plus the mandatory-check plan;
  and
- the exact generation identity.

On reconsideration, the prior generation's structured findings are copied
from signed state into the new Context Bundle with stable digests. The new
result must explicitly retain each exact finding or withdraw it with a bounded
reason; a prior finding can never disappear silently.

Candidate text, repository files, comments, public web content, linked Issue
text, and test output are untrusted data. Candidate changes to instructions or
Review Agent control files never govern their own review.

Incomplete pagination, unreadable content, unsupported changes, merge identity
failure, or a context too large for complete risk coverage yields
`inconclusive`.

## Checks and model

Protected policy fixes the official Codex Action, Codex version,
`moonshotai/kimi-k3`, high reasoning effort, deterministic check catalog, path
rules, budgets, and network fences.

Each head SHA has one signed automatic-review budget. Intent-only edits after
that attempt fail closed as `inconclusive` until a new head arrives or an
authorized bounded reconsideration is accepted.

The model receives no GitHub/App, cloud, deploy, package-publish, or
organization-private credential. It runs from a trusted external session
directory. The pinned Action installs the exact Codex CLI and Responses proxy;
the protected Workflow then runs
`codex exec --dangerously-bypass-approvals-and-sandbox` with CPU,
address-space, and process limits. Codex therefore has full runner-user
filesystem and public-network access without an internal Bubblewrap sandbox.
It inherits no host environment, the job has no Docker socket, and `sudo` is
disabled before the model process starts. The trusted validator compares the
candidate tree before and after the model and rejects any tracked mutation.
Candidate checks execute only through the Check MCP in per-command disposable
worktrees with dedicated HOME/TMP directories and a rootless network namespace
whose own loopback supports local test servers. Configured organization CIDRs
remain blocked inside that candidate-check namespace.

Mandatory checks are selected deterministically from every changed path. The
model may add a check only by protected catalog name through the local stdio
Check MCP. The MCP resolves the immutable command and appends catalog-bound
results to the evidence ledger. Its credential-free stdio handshake runs on
the trusted model host; each resolved check enters the pre-built
private-network namespace before its disposable checkout and filesystem
sandbox start. Codex treats that MCP as required and fails the session if its
protected tools cannot initialize. The validator
rejects unrecorded claims, generation mismatches, incomplete coverage, failed
mandatory checks, invalid findings, excessive output, and unexpected
tracked-file mutation.

The protected bounds admit at most 50 changed files, 128 KiB of captured
change material, 10,000 changed lines, and a 192 KiB encoded Context. This
conservatively enforces the 240,000-token model window and keeps three
concurrent exact-content reads within the repository API budget.

## Verdict and projections

The only decisions are:

- `approved`
- `changes_required`
- `inconclusive`

Trusted code maps them to a normal formal Review and to the App-owned
`Review Agent Verdict` Check:

| Decision | Formal Review | Check conclusion |
| --- | --- | --- |
| `approved` | `APPROVE` | `success` |
| `changes_required` | `REQUEST_CHANGES` | `failure` |
| `inconclusive` | `COMMENT` | `action_required` |

For a control-plane change, an otherwise approved decision remains
`action_required` until a fresh non-author Approval from a protected
`review-agent-owners` login is present.

The Review Agent App owns one mutable status comment, one Check per generation,
one formal Review, and at most 20 inline comments. Only blocking findings are
inline. The complete bounded blocking set remains in the formal Review body.
Projection repair re-reads signed state and refuses duplicate same-generation
Checks or stale heads.

A human `REQUEST_CHANGES` Review remains independently blocking. Review Agent
cannot dismiss it, resolve a thread, or substitute for repository merge
authority.

## Commands

Only one exact, unedited, single-line comment is accepted:

- `@review-agent status` — deterministic signed-state summary, no model;
- `@review-agent explain <question>` — bounded explanation only;
- `@review-agent reconsider <reason>` — new adjudication, at most two per head;
- `@review-agent retry` — write-capable infrastructure recovery;
- `@review-agent cancel` — write-capable cancellation.

The PR author may reconsider; retry and cancel require current `write`,
`maintain`, or `admin` permission. Explain cannot run checks or alter findings,
state decision, Review, or Verdict. Ordinary comments are observed no-ops, so
the App's own status publication cannot recursively trigger work.

## Failure and recovery

Stale workers lose publication authority. Infrastructure failure retries once
inside the protected budget, then becomes `inconclusive`. Code/check failures
are not infrastructure retries. A new commit creates a new generation.
One generation has a 90-minute wall-time budget measured from its signed lease;
the initial review, automatic retry, reconsideration worker, and explanation
worker all honor their own signed lease deadline. Late review results can never
approve and are recorded as `inconclusive`; late explanations are discarded
without changing the verdict. A fresh merge conflict is adjudicated without a
model as `changes_required`, with a formal `REQUEST_CHANGES` Review and failed
Verdict. A failed Controller state, projection, or dispatch effect is
automatically reconciled once from fresh facts.
Every named trusted check is capped at 30 minutes. A review lease reserves up
to 30 minutes for baseline checks, 40 minutes for the reviewer, and 20 minutes
for evidence validation and signed-state publication.

Recovery is event-driven:

- a PR lifecycle event, formal Review, new command, or linked-Issue change
  wakes reconciliation;
- an authorized retry can recover a stuck generation;
- an exact manual dispatch can reconcile one pull request; and
- terminal workers drain the next durable queue entry.

There is deliberately no periodic scan.

## Issue Agent repair loop

Issue Agent PRs are reviewed independently. For the exact current head, the
Issue Agent reads only the latest active formal `CHANGES_REQUESTED` Review
authored by the configured Review Agent Bot and the unresolved threads that
Bot started. That signed digest may wake one of the existing bounded repair
attempts. Human Reviews never acquire `review_agent` authorization, and the
Review Agent never edits the Issue Agent branch itself.

## Repository setup

Provisioning is an external administrative operation and is not performed by
repository code. Follow
[`docs/superpowers/runbooks/review-agent-bootstrap.md`](../superpowers/runbooks/review-agent-bootstrap.md)
before merging the direct replacement.

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
