# GitHub Issue Agent

The Issue Agent behaves like a senior engineer embedded in a GitHub Issue. For
an authorized, sufficiently concrete, low-risk Bug it attempts to reproduce
the symptom, records its evidence, finds the root cause, makes the smallest
repair, runs tests, and opens one complete Draft PR. A human always reviews
and merges.

## Direct flow

```text
Issue event
  -> Controller: fresh facts, authorization, risk, signed state
PR or trusted Review event
  -> credential-free Signal -> default-branch workflow_run Controller
  -> Context Builder: bounded credential-free Context Bundle
  -> Codex Engineer: reproduce -> diagnose -> fix -> focused tests
  -> Candidate capture: filesystem diff against immutable baseline
  -> clean Verifier: apply diff, classify risk, rerun trusted tests
  -> Publisher: exact App-signed commit, Draft PR, state, status
  -> maintainer marks Ready and runs the existing PR validation protocol
  -> human review and merge
```

The Controller derives decisions from current GitHub facts; event payloads are
only wake-up hints. The durable authority for Issue `N` is canonical JSON at
`.issue-agent-state/issue-N.json` on `agent-state/issue-N`. Every state commit
must be contiguous, authored by the configured App Bot, and GitHub-signed.
The Issue contains one mutable App-owned status comment as a repairable
projection of that state.

PR lifecycle and Review events first run
`.github/workflows/issue-agent-pr-signal.yml`. That Workflow has no token
permissions, Secrets, checkout, artifacts, or candidate execution. Its
completion wakes the Controller through `workflow_run`, whose ref and source
are the protected default branch. The Controller accepts only the fixed Signal
Workflow name and one matching `agent/issue-N` PR, then re-reads the actor
permission, unresolved Review threads, PR, Issue, and signed state. Neither
`pull_request_target` nor a PR merge ref receives the Publisher Environment.

The Controller requires its checkout SHA to equal the freshly read protected
`main` head. That exact control SHA is then used by task recovery, Context
Builder, capture, Verifier, and Publisher; candidate code is checked out
separately at the signed task's exact `base_sha`.

## Authorization

An Issue by an `OWNER`, `MEMBER`, or `COLLABORATOR` with current `write`,
`maintain`, or `admin` permission may start automatically. Other reports need
an exact first-line `/agent fix` from an actor whose permission is re-read.
The other commands are:

- `/agent retry` — one fresh ephemeral attempt after `needs_human`;
- `/agent cancel` — stop automatic work;
- `/agent take-over` — give the Agent branch to a human.

Commands in Issue bodies, quoted text, later lines, ordinary comments, or
model output grant no authority.

Only an open Issue using the `[BUG]` title prefix, `bug` label, and all three
required Bug form sections enters engineering. The Controller adds
`ready-for-agent` only while an Issue has active Agent work, and removes it
after the terminal state is durable. A repository-wide serialized five-minute
sweep rotates across at most 40 tracked Issues, so every active task is checked
within the protected four-hour deadline under stable membership; the label
itself grants no authority.

## Codex Engineer boundary

`.github/workflows/issue-agent-engineer.yml` invokes the full-SHA-pinned
official `openai/codex-action` once for the complete task. It runs Codex
`0.146.0`, OpenRouter model `openai/gpt-5.6-sol` through the fixed
`https://openrouter.ai/api/v1/responses` endpoint, high reasoning effort, an
ephemeral session, and a `workspace-write` sandbox with public internet access.
Codex has normal local Git/search/edit/build/test tools, but it receives:

- no GitHub or App token;
- no state-signing material;
- no cloud or deployment credential;
- no Docker socket; and
- no permission to commit, push, open a PR, merge, or deploy.

The Context Bundle separates protected `trusted` authority and limits from
`untrusted` Issue, comment, Review, and public-network text. Codex's
`EngineerResult` is advisory, including its test claims. Every repository
`AGENTS.md` and `FLOW.md` Git blob identity is frozen from the exact candidate
source revision; Codex reads the applicable files before changing a package.
Candidates cannot modify those instruction files.

Before Codex runs, the job freezes a root-owned read-only source baseline.
Afterward a trusted binary compares filesystem contents without trusting
Codex-authored Git refs, index, attributes, ignore rules, or commits.

## Verifier and Publisher

The Verifier applies the captured candidate to an independent clean checkout.
It rejects protected paths, high-risk areas, dependency changes, executable
modes, symlink changes, oversized scope, unexpected
post-test filesystem changes, or failed trusted commands. It has no model key
and no Publisher credential.

Only low-risk passing `CandidateEvidence` can be published. Invalid, missing,
non-ready, or rejected task output is finalized as `needs_human` without
writing candidate code. The scheduled Controller also finalizes any active
task that exceeds the protected four-hour terminal-result deadline.
Immediately before writes the Publisher re-reads the complete signed state
chain, current Issue and actor permission, Agent branch, exact parent tree,
and PR. It writes only:

- `agent/issue-N` using expected-head `createCommitOnBranch`;
- one matching complete Draft PR;
- `agent-state/issue-N`; and
- the one App-owned status comment.

The PR includes root cause, causal path, change summary, trusted commands,
risk, uncertainty, and `Fixes #N`. The Publisher executes no candidate code,
never writes `main`, never force-adopts an external head, never merges, and
never directly closes the Bug.

Protected Issue Agent control paths cannot be modified by the Issue Agent
itself. Such candidates are investigation-only and require a separately
reviewed maintainer change.

## Historical reports

An affected version resolves only to a full repository commit or an immutable
release tag. Blank input freezes to the exact authorization-time `main`.
Invalid or missing identities produce a concrete information request. Codex
receives that exact commit and may use the reviewed helper to build historical
and current `cmd/wukongim` binaries without editing the historical checkout.
Historical diagnosis remains advisory; publication authority comes from the
clean Verifier's tests against the exact repaired base. If Codex cannot obtain
direct evidence from a supported historical build, it must stop at
`needs_human`.

## Repository setup

Create the protected `issue-agent-publisher` Environment without a deployment
approval gate, so authorized low-risk repairs can open PRs automatically.
Configure:

- Environment secret `ISSUE_AGENT_APP_PRIVATE_KEY`;
- Repository secret `OPENAI_API_KEY` containing the dedicated OpenRouter key;
- variables `ISSUE_AGENT_APP_ID`, `ISSUE_AGENT_APP_INSTALLATION_ID`, and
  `ISSUE_AGENT_APP_LOGIN`.

The App installation is limited to this repository and the exact permissions
validated by `internal/infra/issueagentgithub/app_token.go`. Branch protection
must require the existing `Agent Validation Gate`; the Agent cannot merge.

Action or Codex upgrades require a reviewed policy/workflow change updating
the full Action SHA, exact CLI version, contract tests, and this document.

## Local verification

```bash
GOWORK=off go test ./internal/contracts/issueagent \
  ./internal/usecase/issueagent ./internal/runtime/issueagentverify \
  ./internal/infra/issueagentgithub ./internal/access/issueagentcli \
  ./internal/app ./cmd/wkissueagent ./scripts -count=1

GOWORK=off go test -tags=integration \
  ./internal/runtime/issueagentverify -count=1

go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.9 \
  .github/workflows/issue-agent.yml \
  .github/workflows/issue-agent-engineer.yml
```
