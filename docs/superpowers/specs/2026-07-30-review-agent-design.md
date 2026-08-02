# GitHub Review Agent Design

## Problem

The current pull-request validation protocol is safe but operationally
expensive. A maintainer or Agent must inspect the diff, publish a
machine-readable validation-plan comment, reconcile fixed suite-selection
labels, add a one-shot trigger label, monitor a repository-dispatch worker,
and wait for the original merge-gate run to be rerun. The protocol exposes
request, evidence, and gate generations that are useful to its implementation
but difficult for contributors to understand.

WuKongIM instead needs a senior reviewer embedded in each pull request. That
reviewer should understand the requested behavior and repository rules,
inspect the complete change, run the mandatory and risk-selected checks,
participate in review discussion, and publish one authoritative verdict. The
model must not modify or merge the pull request. A protected Publisher may
merge only an exact-head approved PR from an administrator or member.

## Goals

- Automatically review every non-Draft pull request whose base branch is
  `main`, including first-time external Fork pull requests.
- Make one dedicated-App check named `Review Agent Verdict` the only required
  automated merge verdict.
- Submit a normal GitHub Review with actionable inline comments and a concise
  summary for every completed review generation.
- Combine deterministic minimum checks with additional checks selected by the
  Review Agent from the exact pull-request risk.
- Bind every decision to the exact pull request, head SHA, base SHA,
  test-merge SHA, intent digest, and review generation.
- Preserve human authority: the model never modifies code or performs a merge;
  external-author PRs always require a human merge.
- Keep candidate execution, model execution, durable state writes, and
  GitHub Review/Check writes in separate credential boundaries.
- Fail closed on stale work, incomplete diffs, missing evidence, infrastructure
  failure, model failure, or ambiguous intent.
- Replace the existing Agent PR validation protocol completely, with no
  compatibility layer or long-lived dual path.

## Non-goals

- Editing, committing, pushing, rebasing, or otherwise repairing pull-request
  code.
- Automatically merging PRs from contributors who are neither repository
  administrators nor organization members.
- Reviewing Draft pull requests or pull requests targeting unprotected
  branches.
- Running a persistent webhook service, database, or scheduler outside GitHub
  Actions.
- Periodically scanning open pull requests. Recovery is event-driven and
  explicit.
- Allowing pull-request authors to select or skip mandatory checks.
- Reusing the Issue Agent GitHub App, credentials, model session, conclusions,
  or self-test claims.
- Preserving old validation labels, plan comments, statuses, state, or
  Workflow behavior.

## Terminology

- **Review Agent system**: the complete Signal, Controller, Context Builder,
  Policy Evaluator, Verifier, model, Evidence Validator, state writer, and
  Review publisher.
- **Review Agent**: the single ephemeral Codex model session that performs
  semantic code review and selects additional checks.
- **Generation**: one review authority bound to an exact pull-request head,
  base, test-merge commit, and intent digest.
- **Intent digest**: a canonical digest of the pull-request title, body,
  linked Issue/specification identities, and other trusted intent fields used
  by the review.
- **Evidence**: trusted records of the complete changed-file inventory,
  applicable instructions, mandatory checks, additional monitored checks, and
  their real outcomes.
- **Projection**: the GitHub status comment, formal Review, inline comments,
  and Check Run derived from signed state.
- **Control plane**: Review Agent Workflows, policy, prompts, schemas,
  credentials, GitHub App configuration, Rulesets, CODEOWNERS, repository
  `AGENTS.md`/`FLOW.md` instructions, and code that can change Review Agent
  authority.

## Selected Architecture

```text
pull_request_target / Review / comment event
  -> zero-permission Review Agent Signal
  -> protected-default-branch workflow_run Controller
  -> fresh GitHub facts + signed PR state + signed scheduler state
  -> Context Builder: exact diff, intent, instructions, prior review context
  -> Policy Evaluator: mandatory path-based checks
  -> credential-free Verifier: exact test-merge mandatory checks
  -> one ephemeral Review Agent session
       inspect complete risk-classified diff
       inspect mandatory evidence
       run additional monitored checks
       answer with schema-valid findings and one decision
  -> Evidence Validator: real commands/results, coverage, workspace integrity
  -> Review State Writer App: append signed authoritative state
  -> Review Agent App: repair or publish status comment, Review, and Check
  -> exact-head merge for authorized admin/member authors, otherwise human merge
  -> optional second state append recording exact projection identities
```

The design uses GitHub Actions and two dedicated GitHub Apps. It introduces no
always-on external service.

### Proposed repository layout

```text
.github/review-agent/
  policy.json
  review-result.schema.json
  state.schema.json
  prompts/review.md
.github/workflows/
  review-agent-pr-signal.yml
  review-agent-issue-signal.yml
  review-agent.yml
  review-agent-run.yml
cmd/wkreviewagent/
cmd/wkreviewcheck/
internal/access/reviewagentcli/
internal/access/reviewagentcheckmcp/
internal/contracts/reviewagent/
internal/usecase/reviewagent/
internal/runtime/reviewagentverify/
internal/infra/reviewagentgithub/
internal/app/review_agent.go
```

The standalone composition root is not called by `internal/app.New`, does not
join a WuKongIM cluster, and owns no product-server lifecycle. The layer
responsibilities mirror the proven Issue Agent boundaries without sharing its
domain state:

- `access/reviewagentcli` is a strict JSON-only command boundary.
- `access/reviewagentcheckmcp` exposes only named policy checks to the model
  and records their trusted results outside the read-only model session.
- `contracts/reviewagent` owns bounded cross-job objects and schemas.
- `usecase/reviewagent` owns deterministic lifecycle, authorization, commands,
  state transitions, policy decisions, and publication plans.
- `runtime/reviewagentverify` owns credential-free inventory, command
  execution, network fences, and evidence validation.
- `infra/reviewagentgithub` owns bounded GitHub reads, signed state refs, App
  tokens, Review/Check projections, and Ruleset-aware fences.
- `app/review_agent.go` is the only composition root for the CLI operations.

## Event and Wake-up Model

### Zero-permission Signal

`review-agent-pr-signal.yml` is a credential-free event adapter. The pull
request lifecycle trigger uses `pull_request_target` so first-time Fork pull
requests are not held behind contributor Workflow approval. This use is
strictly metadata-only:

- top-level and job-level `permissions` are empty;
- it receives no Secrets or protected Environment;
- it performs no checkout;
- it creates no artifact or cache;
- it executes no pull-request content;
- it does not trust event fields as authority;
- it records only the event kind and completes.

The same Signal Workflow receives bounded formal Review wake-ups and only
newly created top-level pull-request comments whose first bytes are
`@review-agent`. Its successful completion wakes
`review-agent.yml` through `workflow_run`, which GitHub binds to the protected
default branch. The Controller re-reads all authority from GitHub; the Signal
payload is only a hint.

The lifecycle event set covers:

- `opened`, `edited`, `synchronize`, `reopened`, and `closed`;
- `converted_to_draft` and `ready_for_review`;
- Review `submitted`, `edited`, and `dismissed`;
- a newly created top-level pull-request command comment; and
- an explicitly linked Issue's `edited`, `closed`, and `reopened` events,
  resolved by the separate bounded `review-agent-issue-signal.yml`.

No scheduled Workflow or Cron inventory exists. If an event is missed, the
pull request remains blocked until another relevant event or an authorized
`@review-agent retry`/exact manual dispatch wakes the Controller.

### Eligibility

The Controller starts model work only when all of these are true:

- the pull request is open;
- it is not a Draft;
- its base branch is `main`;
- GitHub exposes a complete current head, base, and test-merge identity;
- no terminal state already covers the exact generation;
- a per-PR and repository scheduler slot is available.

Draft, closed, wrong-base, malformed, or superseded work is handled without a
model call. Merge conflicts produce `changes_required`. Missing test-merge
identity, truncated facts, and oversized changes produce `inconclusive`.

### Invalidation and generations

Every generation includes at least:

```text
repository_id
pull_request_number
head_sha
base_sha
test_merge_sha
intent_digest
generation_number
state_parent_sha
```

A new head immediately supersedes and cancels older work for that pull
request. An older worker may finish, but no state or projection publisher may
write for it after fresh GitHub facts disagree.

An intent change invalidates the previous verdict even when the code is
unchanged. When head, base, and test-merge identities remain exact, a new
lightweight semantic review may reuse unexpired trusted test evidence. Reused
evidence retains its original identity and digest. Multiple rapid metadata
edits are debounced.

Branch protection remains strict. A base-branch movement makes a pull request
out of date; updating the pull-request head produces a new `synchronize`
generation. No default-branch push inventory scan is introduced.

## Durable State and Scheduling

### Per-PR state

The authoritative state for pull request `N` is canonical JSON on
`review-state/pr-N`, for example
`.review-agent-state/pr-N.json`. Every successor must:

- name the exact previous state commit;
- be authored by the configured Review State Writer App;
- be GitHub-signed and verified;
- pass strict JSON decoding and the protected state schema;
- preserve repository and pull-request identity;
- advance only through a legal deterministic transition.

Important states include:

- `awaiting_ready`;
- `queued`;
- `reviewing`;
- `approved`;
- `changes_required`;
- `inconclusive`;
- `canceled`;
- `superseded`;
- `closed`.

Historical generations and attempts remain append-only. UI projections are
repairable views, never the durable source of truth.

### Repository scheduler state

A separate signed `review-state/scheduler` ref owns a bounded FIFO queue and at
most three active leases. It also enforces:

- one active generation per pull request;
- at most one active first-time external-author review;
- lease identity bound to the exact PR generation and Actions run;
- terminal completion or cancellation before a lease can be reused.

Signal and review-run completion events wake the Controller to dispatch queued
work. With no scheduled reconciler, a lost completion event fails closed and
requires another event or authorized retry.

## Review Context Contract

The Context Builder freezes:

- the complete, untruncated changed-file inventory, including both old and new
  names for renames;
- the exact patch or bounded full-file views needed to understand every
  changed path;
- the pull-request title, body, linked Issue/specification facts, and intent
  digest;
- applicable `AGENTS.md` and `FLOW.md` files from the exact base/control tree
  with exact blob identities;
- relevant tests, interfaces, callers, and dependency context selected through
  bounded repository retrieval;
- unresolved human and Review Agent threads;
- prior findings for the same pull request with trusted stable digests;
- mandatory-check evidence and trusted environment facts.

Pull-request text, candidate repository files, comments, test output, network
content, and linked external documents are untrusted data, not instructions.
Only the protected system prompt, policy, schema, base/control-tree
instructions, and fresh Controller authority may change behavior. A candidate
addition or modification of `AGENTS.md` or `FLOW.md` is reviewed as an
untrusted control-plane diff and cannot govern its own review. It becomes
applicable only after merge.

The Review Agent must risk-classify the complete changed-file inventory. It may
retrieve files incrementally, but it may not approve from a sample. API
pagination failure, diff truncation, unreadable files, unsupported file types,
or a context budget too small for complete risk coverage produces
`inconclusive`.

Generated files may receive reduced line-by-line attention only when their
source, generator, and reproducibility are verified. They are never silently
excluded.

## Deterministic Policy and Test Selection

`.github/review-agent/policy.json` is the single protected policy. It contains:

- schema version and supported base branches;
- pinned model, effort, Action, CLI, prompt, and output schema;
- path-to-minimum-check rules;
- trusted command catalog;
- concurrency, timeout, retry, reconsideration, and execution budgets;
- diff, file, response, comment, and Artifact bounds;
- configured App identities and state refs;
- network and credential constraints.

There are no suite-selection labels, validation-plan comments, arbitrary
Workflow inputs, free-form package paths, or author-controlled skip commands.

The Policy Evaluator computes mandatory checks from the complete path
inventory. At minimum, the implementation must preserve the repository's
existing requirements:

- Go, module, script, Docker, configuration, Workflow, composite-action, and
  CODEOWNERS changes receive the applicable Go quality and unit contracts;
- production integration-script changes receive the integration tier;
- Manager Web changes receive lint, tests, type checking, build, and tracked
  bundle verification;
- Chat Demo changes receive its tests, build, and tracked bundle verification;
- documentation-only classification is exclusive and path-allowlisted;
- race, integration, E2E, and three-node-cluster checks are selected from
  actual risk, not a contributor label.

The Review Agent may request additional bounded checks. A local stdio Check
MCP accepts only a named check from the
protected catalog, resolves the fixed command itself, and records the result
outside the read-only model session. Only commands and outcomes captured by
that trusted execution boundary count as formal evidence. Model-authored
claims that a command ran or passed are advisory.

## Review Agent Session

Each generation uses one ephemeral Codex session:

- initial model: `moonshotai/kimi-k3`;
- reasoning effort: `high`;
- official Codex Action and CLI pinned to reviewed immutable versions;
- no inherited Issue Agent or previous Review Agent hidden context;
- a trusted external session directory and candidate checkout that are both
  read-only to the model; build tools run only in Check MCP-created disposable
  worktrees;
- no GitHub, App, cloud, deploy, package-publish, or organization credential;
- no Docker socket and no `sudo`;
- complete public-internet egress, while RFC1918, link-local, cloud metadata,
  runner-host, and organization-private targets remain blocked by the model
  profile; one root-owned Responses proxy is the sole model-transport loopback
  exception, clamps `max_output_tokens` to 32,768, injects the OpenRouter
  credential after deleting its root-only handoff file, and exposes no
  unclamped transport path to runner-user Codex; candidate checks receive only
  namespace-local loopback;
- bounded wall time, CPU, memory, process count, connection count, and network
  volume.

The Agent may inspect code, compile, run tests, perform static analysis, and
query public sources. It must not intentionally edit the pull-request
implementation. Any workspace change is discarded. Unexpected tracked-file
changes after review invalidate evidence and produce `inconclusive`.

The session receives mandatory results before its decision and can request
additional monitored checks during the same session. A second model
adjudication pass is not required.

## Review Contract

The Review Agent evaluates four dimensions:

1. **Intent and correctness**: implementation versus pull-request description,
   linked Issue/specification, invariants, and existing behavior.
2. **Regression and tests**: mandatory and selected check results, coverage of
   changed behavior, and missing regression cases.
3. **Security and runtime risk**: permissions, secrets, dependencies,
   concurrency, bounded resources, compatibility, fanout, backpressure, and
   expected WuKongIM scale.
4. **Repository constraints**: applicable `AGENTS.md`, `FLOW.md`, architecture
   direction, configuration, documentation, and test-tier rules.

If intent is not sufficiently concrete, the Agent returns `inconclusive` and
states the missing information instead of inventing a requirement.

### Blocking threshold

The Agent must block:

- reproducible test, build, static-analysis, or race failures attributable to
  the change;
- correctness, data-loss, compatibility, security, or permission defects;
- concurrency races or unbounded CPU, memory, allocation, contention, queue,
  retry, or fanout behavior;
- violations of mandatory repository rules;
- high-confidence defects with a concrete production failure path even when no
  existing test reproduces them.

Naming preferences, style disputes, optional refactors, speculative
abstractions, and nonessential comments are advisory. Serious but unproven risk
is `inconclusive`, not `changes_required`.

Every blocking finding contains:

- a tight file and line location when possible;
- the failing scenario;
- concrete impact;
- supporting evidence;
- a verifiable condition for resolving it.

## Decisions and GitHub Projection

The Review Agent emits exactly one schema-valid decision:

| Decision | Formal Review | `Review Agent Verdict` |
| --- | --- | --- |
| `approved` | `APPROVE` | `success` |
| `changes_required` | `REQUEST_CHANGES` | `failure` |
| `inconclusive` | `COMMENT` | `action_required` |

Only the validator-confirmed `approved` decision may produce a successful
required Check. A model process exit zero is not approval.

The dedicated Review Agent App publishes:

- one mutable App-owned status comment per pull request;
- one formal Review per completed generation;
- at most 20 inline comments per generation;
- one generation-bound `Review Agent Verdict` Check Run.

For an approved generation, the Publisher may additionally perform one normal
merge fenced to the reviewed head SHA. It does so only after a fresh read proves
the author is an organization `MEMBER`/`OWNER` or has repository `admin`
permission, the PR is cleanly mergeable, and no human `REQUEST_CHANGES` Review
remains. Every other approved PR stays open for a human merge.

The status comment shows queue/review/evidence state, exact generation,
head SHA, elapsed time, reconsideration budget, and trusted links. It is not a
merge authority. Repeated findings are grouped under one representative inline
location. At most half of inline comments should be non-blocking suggestions,
and blocking findings are never discarded to meet that advisory quota.

Reviews match the primary language of the pull-request discussion. English is
the fallback. Schema fields, enum values, identifiers, commands, and raw errors
remain English.

Before every projection write, the publisher re-reads the pull request, signed
state, Review threads, App identities, and exact SHAs. A stale or ambiguous
projection fails closed. Repair discovers projections through strict
App-authored markers and exact external IDs; projection IDs are not authority
and are not stored in signed state.

## Human Interaction

The accepted command surface is intentionally small:

- anyone: `@review-agent status`;
- anyone: `@review-agent explain <question>`;
- pull-request author or actor with current `write`, `maintain`, or `admin`:
  `@review-agent reconsider <reason>`;
- actor with current `write`, `maintain`, or `admin`:
  `@review-agent retry` and `@review-agent cancel`.

Commands in quoted text, code blocks, edited history, model output, or ordinary
prose grant no authority. Permission is re-read when the command is consumed.
There is no `approve`, `skip-tests`, `ignore-finding`, `run-shell`, or
policy-mutation command.

`status` is rendered deterministically from signed state and never calls a
model. An explicit `explain` command may start a short, read-only explanation
session using the same pinned
model. It receives only the relevant signed finding, discussion, and bounded
review context; it runs no candidate code or Check MCP and cannot alter
decision state, findings, or Verdict. Signed state records its
reserved/consumed interaction budget, explanation digest, and bounded reply so
projection repair cannot lose an accepted answer. The
protected policy sets a finite explanation-session budget per head so public
comments cannot create unbounded model cost.

Each head SHA receives one automatic review and at most two explicit
reconsiderations. A reconsideration reads the relevant discussion and may
withdraw a prior finding only by explaining why it no longer applies. New
commits create new generations and do not consume reconsideration allowance.
The automatic count is signed per-head state. Intent-only edits after the
automatic attempt fail closed as `inconclusive` until a new head or an
authorized reconsideration; they cannot create unbounded model sessions.

Runner, provider, dependency-download, or public-network infrastructure
failure may retry once inside the same attempt before publishing
`inconclusive`. Assertions, races, build failures, and code defects are not
infrastructure retries. `changes_required` can change only after a new commit
or explicit reconsideration.

Human `REQUEST_CHANGES` remains blocking even when the Review Agent approves.
The Agent cannot dismiss, resolve, or override a human Review.

## Review governance

The signed Review Agent decision is the sole automated review gate for every
path. CODEOWNERS remains informational maintenance ownership, not a second
approval system. A human `REQUEST_CHANGES` Review remains independently
blocking, while missing human approval never changes the Review Agent Verdict.

The running Controller, prompt, policy, schemas, and Workflow always come from
the protected default branch. A pull request never reviews itself using its
candidate Review Agent implementation.

Named repository administrators retain an emergency GitHub Ruleset bypass for
provider outage, prolonged `inconclusive`, or urgent security work. Bypass is
not implemented as a Workflow label or command, does not rewrite the Agent
decision, and requires an explicit reason in the pull request and GitHub audit
trail.

## Credential and App Boundaries

Two independent GitHub Apps are required.

### Review Agent App

The Review Agent App may read metadata and pull-request facts and write the
Review, comments, Check, and exact-head merge surfaces. GitHub requires
`contents:write` for the merge endpoint; the protected adapter exposes no
generic contents, branch, or commit write. The App has no Ruleset, Actions
administration, or Secrets permission.

Its private key is available only in the protected Review Publisher
Environment. The Publisher never checks out or executes candidate code.
That Environment permits deployment only from protected `main`; tags, custom
branch patterns, and pull-request refs are denied.

### Review State Writer App

The Review State Writer App has the minimum Git permission needed to append
signed state commits. It cannot write Reviews, comments, or Checks. Its private
key is available only in a separate protected State Writer Environment.
That Environment also permits deployment only from protected `main`.

Because GitHub `contents: write` cannot be scoped to one ref, repository
Rulesets must deny this App creation or update access to `main` and all
non-state branches while allowing only `review-state/**`. The state writer
accepts no caller-selected ref or path.

The two Apps do not share private keys, installation tokens, Environments, or
jobs. Every installation token is short-lived and repository-scoped.

## Issue Agent Integration

An exact `agent/issue-<number>` pull request is reviewed under the same policy
as any other pull request. The Review Agent receives a fresh independent
session and does not trust Issue Agent evidence or conclusions.

When the Review Agent submits `REQUEST_CHANGES`:

1. the existing credential-free Issue Agent PR Signal wakes its Controller;
2. the Issue Agent reads the blocking Review findings;
3. it may publish one repair commit to its existing Agent branch;
4. the new head starts a new Review Agent generation.

The loop is bounded by the Issue Agent's existing maximum of two Review repair
iterations. Exhaustion requests human help. The Review Agent never calls the
Issue Agent directly and never modifies the Agent branch.

## Budgets and Retention

Initial hard budgets are:

- one active generation per pull request;
- at most three active Review Agent sessions repository-wide;
- at most one active first-time external-author session;
- 90 minutes wall-clock per complete generation;
- one automatic initial review per head;
- at most two explicit reconsiderations per head;
- one automatic infrastructure retry per signed review generation.

Exact context-token and response-byte limits, per-process CPU and memory
limits, per-command process limits, per-address-family connection and
network-volume limits, changed-file/byte/line limits, and explanation-session
count are protected policy. Provider spend controls remain an Environment
provisioning concern; repository policy bounds exposure through fixed model,
concurrency, attempts, and wall time. Reaching any hard runtime budget produces
`inconclusive`; it never reduces review depth or silently approves.
The model Environment is likewise restricted to protected `main`, with tags
and custom branch patterns disabled.

Retention is:

- signed state and its evidence digests: permanent;
- GitHub Reviews, comments, and Checks: normal GitHub pull-request history;
- successful detailed Artifacts: 7 days;
- `changes_required` or `inconclusive` detailed Artifacts: 30 days;
- Actions logs: the repository's current 90-day setting.

Artifacts must not contain credentials, environment dumps, private data, or
unbounded network responses.
Artifact retention follows the validated Review decision, not whether the
model Action process itself exited successfully.

## Branch Protection

After migration, `main` must:

- require only `Review Agent Verdict` from the exact Review Agent App as its
  automated status check;
- require strict up-to-date status;
- apply protection to administrators;
- do not require Code Owner Approval; CODEOWNERS is maintenance metadata;
- preserve blocking human `REQUEST_CHANGES`;
- reserve native Ruleset bypass for the named emergency administrators.

A same-named Check or commit status from GitHub Actions or another App must not
satisfy branch protection.

## Direct Replacement

The replacement deliberately has no compatibility layer.

One migration pull request:

- adds the complete Review Agent system and its tests;
- deletes `agent-pr-merge-gate.yml`,
  `agent-pr-validation-control.yml`, and `agent-pr-validation.yml`;
- deletes the validation-plan parser/script and obsolete contract tests;
- removes all documentation for the plan-comment and suite-label protocol;
- updates Issue Agent handoff documentation to the Review Agent contract.

The migration pull request is the last change protected by the old gate. After
merge, old required checks cannot be produced, so merges temporarily freeze.
The two Apps, Environments, keys, and Rulesets are provisioned before that
merge.

An authorized operator then:

1. manually dispatches exact reconciliation for every open non-Draft `main`
   pull request;
2. confirms new Checks are published by the configured Review Agent App;
3. atomically replaces the old required check with `Review Agent Verdict`;
4. deletes all repository `agent-ci/*` labels.

Existing pull requests start fresh Review Agent state and review. Old plan
comments, labels, statuses, and gate results remain only as historical GitHub
records and are neither translated nor consumed.

There is no long-running Shadow phase. Before the branch-protection switch, one
same-repository bootstrap pull request and one Fork bootstrap pull request must
prove the complete path.

## Verification Strategy

### Static Workflow and policy contracts

- parse every Workflow as YAML;
- require all third-party Actions to use full immutable SHAs;
- require the Signal to have no permissions, Secrets, checkout, cache,
  artifacts, or candidate execution;
- require model and Verifier jobs to have no write credential or protected
  Environment;
- require base/control-tree `AGENTS.md` and `FLOW.md` to govern review while
  candidate changes to those files remain untrusted control-plane diffs;
- require Review and state writes to use separate jobs, Apps, and Environments;
- require the Publisher to consume only sanitized schemas and never execute
  candidate code;
- reject arbitrary commands, refs, paths, App identities, and Workflow inputs;
- prove old validation Workflows, labels, and plan protocol are absent.

### State and event matrix

- opened, Draft, ready, edited intent, synchronize, reopened, closed;
- same-repository and first-time Fork pull requests;
- Review submitted, edited, dismissed;
- newly created top-level command comments and linked-Issue changes;
- duplicate, reordered, and missing hints;
- stale worker completion and new-generation cancellation;
- exact same-head reconsideration limits;
- queue lease release, three-session cap, and external-author cap;
- malformed, unsigned, discontinuous, wrong-App, or wrong-ref state;
- projection write failure and idempotent repair;
- no-event recovery through explicit retry/manual exact dispatch.

### Review and evidence contracts

- complete inventory and rename accounting;
- instruction discovery and exact blob identity;
- mandatory path classification and Agent-added checks;
- no partial approval for truncated or oversized diffs;
- real command/exit evidence versus model-authored claims;
- tracked-workspace mutation detection;
- three-state decision mapping;
- approved control-plane changes need no human Approval;
- human `REQUEST_CHANGES` preservation;
- language selection and inline-comment bounds.

### Security integration

- first-time Fork code executes without any App, GitHub write, cloud, deploy,
  package-publish, or organization credential;
- public internet works while private, metadata, runner-host, and organization
  ranges are unreachable;
- `sudo` and Docker socket access are absent;
- Publisher jobs never download or execute candidate trees;
- State Writer is rejected from every non-`review-state/**` ref by Rulesets;
- Review Agent adapter cannot create commits or branches and can merge only an
  exact approved head from an authorized administrator/member author;
- malicious instructions in code, comments, tests, and network responses
  cannot change authority or policy.

### Bootstrap

Before branch protection changes:

- one same-repository pull request must prove the full approved path;
- one Fork pull request must prove automatic wake-up, credential isolation,
  exact generation binding, and approved projection;
- seeded failing and infrastructure-failure fixtures must prove
  `changes_required` and `inconclusive`;
- a stale generation must fail to publish after a new commit;
- required-check configuration must name the exact Review Agent App ID.

## Acceptance Criteria

1. Every eligible non-Draft `main` pull request, including a first-time Fork,
   starts automatically from a zero-permission event hint.
2. No candidate-controlled Workflow, file, comment, or network response can
   grant authority, change policy, or reach a Publisher credential.
3. One complete generation reviews the full changed-file risk inventory and
   produces trusted mandatory and selected-check evidence.
4. Only schema-valid `approved` state for the exact current generation can
   produce a successful `Review Agent Verdict`.
5. `changes_required`, `inconclusive`, missing evidence, stale work, and human
   requested changes all block merging.
6. The Review Agent produces useful formal Reviews and bounded inline comments
   without modifying or merging code.
7. New commits and changed intent invalidate older decisions; late workers
   cannot overwrite newer state or projections.
8. Review and state credentials are held by different Apps and jobs, and
   neither credential is exposed to the model or candidate execution.
9. Issue Agent pull requests enter a bounded independent review/fix loop.
10. All existing Agent PR validation Workflows, scripts, labels, plan comments,
    documentation, and branch-protection requirements are removed rather than
    adapted.
11. Open pull requests receive fresh state and new Review Agent decisions
    before the required-check switch.
12. Static contracts, event/state tests, security integration, same-repository
    bootstrap, and Fork bootstrap all pass before direct replacement.

## Calibration Required Before Enablement

The following are implementation measurements, not open product decisions:

- maximum changed files, bytes, and lines per generation;
- maximum Context Bundle and model response sizes;
- exact context-token and model-response byte budgets;
- provider-side spend controls for the dedicated model credential;
- maximum explanation sessions and response bytes per head;
- trusted command catalog and timeout per command;
- network connection, bandwidth, and process limits;
- concrete Review Agent App, Review State Writer App, and Environment
  identifiers.

Any chosen value must remain in the protected policy, fail closed at its
boundary, and be covered by contract tests.
