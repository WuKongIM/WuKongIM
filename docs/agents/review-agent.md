# GitHub Review Agent

The Review Agent is an administrator-invoked senior reviewer for pull requests
targeting `main`. The model reviews and adjudicates without changing code or
merging. No model review starts until a current repository administrator posts
the exact `@review-agent review` command.

## Lifecycle

```text
administrator @review-agent review
  -> Review Agent PR Signal (zero permission)
  -> Review Agent Controller (fresh GitHub facts)
  -> signed per-PR state + signed repository scheduler
  -> Context Builder + deterministic baseline checks
  -> ephemeral Review Agent model + protected named Check MCP
  -> trusted evidence validator
  -> State Writer App
  -> Review Agent App projections
  -> authorized exact-head merge or human merge
```

The Signal uses `pull_request_target` so lifecycle changes can cancel stale
work or repair state without contributor approval. It does not checkout,
execute, upload, download, call the network, or receive a token or Secret.
Lifecycle, Review, and manual-dispatch signals cannot create a generation.
Controller authority to start review comes only from an exact command plus a
fresh GitHub API read proving the comment author currently has `admin` access.

The eligible set is every administrator-requested open, non-Draft pull request
whose base is `main`.
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

Each Controller run compiles its protected `wkreviewagent` binary once. A
state-writing or projection path passes that exact binary through a
control-SHA-bound, SHA-256-verified run Artifact. When reconciliation proves
that state, scheduler, projection, cancellation, and dispatch are all
unchanged, the credentialed State Writer and Publisher plus the Dispatcher are
not started.

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
`deepseek/deepseek-v4-flash`, high reasoning effort, deterministic check catalog, path
rules, a 32,768-token maximum model output, and network fences. One root-owned
loopback proxy applies that ceiling and the OpenRouter credential; its
root-only credential handoff is deleted before Codex starts, so no unclamped
model transport remains reachable to the runner user.

Each head SHA has one signed initial-review budget. Intent-only edits and new
commits invalidate the old generation but never start another review. An
administrator uses `@review-agent review` for a new head and
`@review-agent reconsider <reason>` for changed same-head facts. A
reconsideration remains eligible after the protected control revision, intent,
base, or test-merge revision changes; it binds a new generation from fresh
eligible facts and consumes the signed per-head reconsideration budget.

The model receives no GitHub/App, cloud, deploy, package-publish, or
organization-private credential. It runs from a trusted external session
directory. The pinned Action installs the exact Codex CLI. The protected
Workflow installs the sole root-owned model proxy, then runs
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

The Worker compiles the three protected Review Agent binaries once from the
exact control SHA into one run-scoped Artifact. Each isolated job verifies the
embedded control SHA and SHA-256 manifest, installs only its role's allowlisted
binaries, and deletes the downloaded bundle. No cross-run cache or candidate
build participates in this trust boundary. Documentation-only changes under
`docs/`, `docs-site/`, `README.md`, or `README_CN.md` exclusively select
`docs-contracts`; mixed changes continue to select the full applicable union.

The protected bounds admit at most 50 changed files, 1 MiB of captured change
material, 30,000 complete-file lines, and a 2 MiB encoded Context. The encoded
byte cap is only an ingestion bound; it is independent of the 240,000-token
model window, 216,000-token automatic-compaction threshold, and 32,768-token
per-request output ceiling. Three concurrent
exact-content reads remain within the repository API budget.

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

The signed Review Agent decision is the sole automated review gate, including
for Review Agent control-plane changes. A human `REQUEST_CHANGES` Review still
blocks independently; missing human approval does not change the Check result.

The Review Agent App owns one mutable status comment, one Check per generation,
one formal Review, and at most 20 inline comments. Only blocking findings are
inline. The complete bounded blocking set remains in the formal Review body.
Projection repair re-reads signed state and refuses duplicate same-generation
Checks or stale heads.

A human `REQUEST_CHANGES` Review remains independently blocking. Review Agent
cannot dismiss it or resolve a thread.

After the approved Review and successful Verdict exist, the Publisher re-reads
the open, non-Draft PR and author authority. Automatic merge requires all of:

- the current PR head, base, test-merge identity, and intent still match the
  signed approved generation;
- GitHub reports the PR as cleanly mergeable;
- no current exact-head human `REQUEST_CHANGES` Review exists; and
- the PR author is an organization `MEMBER`/`OWNER`, or currently has
  repository `admin` permission.

`write`, `maintain`, ordinary collaborator, contributor, Bot, unknown, and
permission-read-failure cases are not auto-merge authority. Their approved PRs
remain open with an explicit human-merge notice. The merge API receives the
exact reviewed head SHA and the normal merge method; repository Rulesets remain
authoritative.

Repository administrators retain manual merge authority for every PR,
including a PR with no Review Agent state or a non-successful verdict. This is
a GitHub governance capability, not an Agent transition.

## Commands

Only one exact, unedited, single-line comment is accepted:

- `@review-agent review` — start the initial review for the current head;
- `@review-agent status` — deterministic signed-state summary, no model;
- `@review-agent explain <question>` — bounded explanation only;
- `@review-agent reconsider <reason>` — new adjudication, at most two per head;
- `@review-agent retry` — infrastructure recovery;
- `@review-agent cancel` — cancellation.

Review, explain, reconsider, retry, and cancel require current `admin`
permission resolved after the comment arrives. Status is public, deterministic,
and model-free. Explain cannot run checks or alter findings, state decision,
Review, or Verdict. Ordinary comments are observed no-ops, so the App's own
status publication cannot recursively trigger work.

## Failure and recovery

Stale workers lose publication authority. A new commit cancels old work and
waits for another administrator review command. Infrastructure failure retries
once inside the protected budget, then becomes `inconclusive`. Code/check
failures are not infrastructure retries. The next administrator review command
creates the new generation.
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

- a PR lifecycle event, formal Review, or exact command wakes reconciliation;
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

node --test .github/review-agent/responses-budget-proxy.test.mjs

go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.9 \
  .github/workflows/review-agent-pr-signal.yml \
  .github/workflows/review-agent.yml \
  .github/workflows/review-agent-run.yml
```
