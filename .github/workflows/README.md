# GitHub Actions tool catalog

This directory contains:

- `Agent Tool - ...`: a bounded, on-demand worker;
- `Safety Automation - ...`: an autonomous controller or safety backstop.

Use the stable filename when invoking a Workflow. Workflows that create cloud
resources, change permissions, or spend money still require explicit user
authorization and the applicable budget.

## Catalog

| File | Display name | Purpose |
| --- | --- | --- |
| `chat-lifecycle-rehearsal.yml` | `Agent Tool - Start Chat Lifecycle Rehearsal` | Builds, quotes, acquires, deploys, and hands a full-scale two-hour rehearsal to remote systemd |
| `chat-lifecycle-rehearsal-finalize.yml` | `Safety Automation - Finalize Chat Lifecycle Rehearsals` | Uploads a terminal rehearsal report before Release and reconciles the Lease to zero inventory |
| `chat-lifecycle-formal.yml` | `Safety Automation - Start Fresh Formal Chat Lifecycle` | Consumes an authenticated released rehearsal transition and starts a fresh 96-hour formal Lease |
| `chat-lifecycle-formal-finalize.yml` | `Safety Automation - Finalize Formal Chat Lifecycle Runs` | Collects the same-Lease Soak/capacity/recovery result before Release and zero-inventory proof |
| `review-agent-pr-signal.yml` | `Safety Automation - Review Agent PR Signal` | Emits a credential-free lifecycle or exact-command wake-up hint |
| `review-agent.yml` | `Safety Automation - Review Agent Controller` | Re-reads GitHub facts and signed state, then plans one lifecycle transition |
| `review-agent-run.yml` | `Agent Tool - Review Pull Request` | Runs one exact review or explanation generation |
| `issue-agent-pr-signal.yml` | `Safety Automation - Issue Agent PR Signal` | Emits credential-free lifecycle and Review hints for Issue Agent PRs |
| `issue-agent.yml` | `Safety Automation - GitHub Issue Agent` | Reconciles Issue work and Review Agent repair requests |
| `issue-agent-engineer.yml` | `Agent Tool - Issue Engineer` | Runs one exact Context Builder, Codex Engineer, and clean Verifier chain |
| `cloud-lease-oidc-setup.yml` | `Agent Tool - Configure Cloud Lease OIDC Roles` | Reconciles and live-verifies the three workflow-conditioned Cloud Lease roles |
| `cloud-lease-provision.yml` | `Agent Tool - Provision Cloud Lease` | Quotes or explicitly acquires one generic Alibaba Cloud Lease |
| `cloud-lease-observe.yml` | `Agent Tool - Inspect Cloud Lease` | Reconstructs exact Lease inventory through the read-only Observer role |
| `cloud-lease-release.yml` | `Safety Automation - Release Cloud Leases` | Releases one exact Lease and runs the protected 15-minute expired/cleanup-pending repository sweep |
| `cloud-deployment-bundle.yml` | `Agent Tool - Build Cloud Deployment Bundle` | Builds and seals one procurement-independent offline Ubuntu four-host payload |
| `cloud-deployment-activate.yml` | `Agent Tool - Activate Cloud Deployment` | Installs and gates one exact offline bundle on an active four-host Lease |
| `cloud-sim-provision.yml` | `Agent Tool - Provision Cloud Simulation` | Creates a leased Alibaba Cloud Simulation Run |
| `cloud-sim-analyze.yml` | `Agent Tool - Analyze Cloud Simulation` | Operates one bounded cloud analysis session |
| `cloud-sim-oidc-subject.yml` | `Agent Tool - Configure Cloud Simulation OIDC Subject` | Configures and verifies the cloud OIDC subject |
| `cloud-sim-cleanup.yml` | `Safety Automation - Reconcile Cloud Simulation Resources` | Destroys expired cloud leases and supports exact cleanup |
| `cloud-sim-monitor.yml` | `Safety Automation - Patrol Cloud Simulation Runs` | Patrols retained live runs and records bounded health evidence |

## Review Agent

Every pull request defaults to human handling. A model review starts only after
a repository administrator posts the exact `@review-agent review` command:

```text
administrator @review-agent review
  -> zero-permission Signal
  -> protected-default-branch Controller
  -> fresh GitHub facts + signed PR state + signed scheduler
  -> exact context + deterministic checks + one ephemeral model session
  -> evidence validation + signed terminal state
  -> status comment + formal Review + Review Agent Verdict
  -> exact-head auto-merge for a repository admin or organization member
     | otherwise wait for a human merge
```

Other PR and Review events may close state, cancel stale work, or repair a
projection, but cannot create a review generation. A new commit invalidates
and cancels any old work; an administrator must explicitly review the new head.

The Signal is a hint, not authority. It has no token permission, Secret,
checkout, Artifact, cache, network command, or candidate execution. The
Controller always re-reads the pull request and never checks out candidate
code.

`review-agent-run.yml` separates these authorities:

- candidate checkout and trusted checks use credential-free jobs;
- `review-agent-model` exposes only `OPENAI_API_KEY`;
- `review-agent-state-writer` exposes only the State Writer App key;
- `review-agent-publisher` exposes only the Review Agent App key;
- the isolated dispatcher has only `actions: write`.

The Worker validates and checks out the exact protected control revision once,
builds `wkreviewagent`, `wkreviewcheck`, and `wkreviewcheckmcp` in one job, and
publishes one run-scoped Artifact. Every consuming job verifies the embedded
control SHA and complete SHA-256 manifest, installs only its allowlisted
binaries, and removes the downloaded bundle before continuing. Candidate code
cannot build, replace, or upload this bundle, and no shared build cache crosses
Worker runs.

The Controller also compiles `wkreviewagent` only once per run. When a plan
requires a state write or projection, credentialed jobs consume a run-scoped,
control-SHA-bound manifest Artifact instead of rebuilding it. A true no-op
uploads only its bounded plan; State Writer, Publisher, and Dispatcher jobs are
skipped.

The model can review and invoke only protected named checks. It cannot edit the
PR, commit, push, merge, resolve threads, dismiss Reviews, or publish its own
verdict. A trusted validator maps signed state to the sole required Check
`Review Agent Verdict`. Only `approved` maps to success. `changes_required`
maps to failure, and `inconclusive` maps to `action_required`. A human
`REQUEST_CHANGES` Review remains independently blocking.

The model request passes through one root-owned loopback proxy that clamps
`max_output_tokens` to the protected policy and injects the OpenRouter
credential. The root-only credential handoff file is deleted before the
listener is published. Codex and candidate checks cannot read the credential,
replace the proxy, or reach an unclamped transport path.

After publishing an `approved` verdict, the protected Publisher re-reads the
exact PR head, mergeability, human Reviews, author association, and author
repository permission. It merges only when the author is an organization
`MEMBER`/`OWNER` or currently has repository `admin` permission. Every other
approved PR remains open and is marked as requiring a human merge. The merge
request is fenced to the reviewed head SHA and still obeys repository rules.
Repository administrators retain GitHub's manual merge authority for every PR
whether or not Review Agent was invoked or produced a verdict.

The signed lease bounds the complete generation to 90 minutes. Infrastructure
failure is retried once inside that same generation and deadline; a late result
is forced to `inconclusive`. A merge conflict bypasses the model and publishes
`changes_required`. Candidate baseline commands run only after the shared
network fence disables both Docker access and `sudo`. Candidate checks receive
isolated loopback inside a rootless network namespace whose host loopback is
disabled. The trusted baseline host keeps runner transport available only so
pinned post-job Actions can upload evidence; candidate code never runs there.
The model host keeps GitHub runner transport intact. The pinned Codex Action
installs the exact CLI, then the Workflow invokes
`codex exec --dangerously-bypass-approvals-and-sandbox` under model-only CPU,
address-space, and process limits. This gives Codex full runner-user filesystem
and public-network access without its internal Bubblewrap sandbox. The model
receives no GitHub or App credential, inherits no host environment, and starts
only after Docker and `sudo` are disabled. Candidate check commands still run
inside the rootless network namespace and their Bubblewrap sandboxes. The
trusted validator rejects any tracked candidate-tree mutation.

Ubuntu AppArmor may restrict unprivileged user namespaces on hosted runners.
Each candidate runner installs one root-owned Review Agent `unshare` copy and
loads a path-specific profile granting only `userns`; the global restriction
is never disabled. After the namespace and its network rules are ready, the
job unloads the temporary profile and removes both the copied binary and its
directory before any candidate command can run. Private-CIDR, quota, and
connection fences live inside the candidate namespace; Docker and `sudo` are
disabled on the trusted baseline host without blocking its Artifact transport.
Explanation-only sessions do not install that profile or create a candidate
network namespace because they never execute candidate checks. The Check MCP
is a required Codex dependency. Its credential-free stdio server completes the
Codex handshake on the trusted model host, while each resolved protected check
enters the pre-built private-network namespace before its disposable checkout
and bubblewrap sandbox start.
Failure to initialize the MCP stops the model session instead of silently
removing the protected check tools.

The exclusive documentation fast path covers `docs/`, `docs-site/`,
`README.md`, and `README_CN.md`. It runs `docs-contracts` without falling
through to repository-default Go checks; any mixed or non-allowlisted change
still receives the union of its applicable checks.

Worker dispatch is serialized per pull request. The exact run title derived
from pull request, signed lease, and infrastructure attempt is the idempotency
key at both Controller and retry-drain boundaries, so concurrent recovery
cannot start the same attempt twice.

Review-request and interaction budgets are signed per head SHA. An
authorized reconsideration of the current head binds a new generation from
fresh eligible facts even when the protected control revision, intent, base,
or test-merge revision changed; it consumes reconsideration allowance.

Missing Context, reviewer, or trusted-baseline artifacts are evidence of an
infrastructure failure, not reasons to abort the state machine. The Evidence
job records the bounded retry or terminal `inconclusive` completion so signed
state and the repository queue always advance.
Before validation, that job normalizes the bounded model output through the
Go ReviewResult decoder. Shell code does not implement a second JSON-shape
policy.

There is no scheduled Review Agent scan. A failed Controller effect is retried
once; other recovery comes from a later event or an exact manual Controller
dispatch.

See [`docs/agents/review-agent.md`](../../docs/agents/review-agent.md) for the
state model, commands, security boundaries, and repository setup.

## Issue Agent integration

The Issue Agent remains an engineering agent and still cannot merge. When the
latest exact-head formal Review from the configured Review Agent Bot is
`CHANGES_REQUESTED`, only its unresolved findings may authorize one of the
Issue Agent's bounded repair loops. Human Review comments remain human
authority and never masquerade as an automated repair command.
Its reusable workflow ends after the clean Verifier; the caller-owned
Publisher separately finalizes every accepted task from the protected
`issue-agent-publisher` Environment, including failed engineering attempts.
For a Ready Agent PR whose exact App-owned candidate is behind `main`, that
same Publisher may mechanically recreate the candidate on current `main` only
when none of its paths changed. The expected-head ref swap advances signed
state, consumes a bounded sync attempt, invalidates prior Review authority, and
requires a fresh `Review Agent Verdict`; overlaps and external heads fail
closed without asking Codex to merge them.

See [`docs/agents/issue-agent.md`](../../docs/agents/issue-agent.md).

## Cloud Simulation

Cloud creation and permission changes remain explicit Agent Tools. The older
Cloud Simulation's cleanup and live-run patrol remain its scheduled safety automations. Provider
credentials, analysis credentials, and cleanup authority stay in their
documented separate Environments.

See
[`docs/superpowers/runbooks/cloud-simulation.md`](../../docs/superpowers/runbooks/cloud-simulation.md).

## Cloud Lease identity and lifecycle

The generic Cloud Lease flow is distinct from the older Cloud Simulation
identity. One local administrator-authenticated setup command preserves
unrelated Environment settings, removes human-review requirements from the
four unattended Environments, and configures the repository OIDC subject. The
setup Workflow then uses the existing complete Alibaba AccessKey Secret pair
only when the seven non-secret binding Variables are absent or a forced repair
is requested. It creates no Lease infrastructure and leaves those Secrets
untouched.

Successful setup requires live OIDC exchange plus exact sole-policy, one-hour
session, setup-subject, and ordinary-workflow-subject verification for
CloudLeaseProvisioner, CloudLeaseObserver, and CloudLeaseReleaser. Ordinary
Quote/Acquire, Inspect, and Release/Sweep tools then use only their corresponding
short-lived role. Deployment uses `cloud-deployment`, receives no `id-token`
permission, and has no Alibaba credential. See
[`docs/superpowers/runbooks/cloud-lease-identity.md`](../../docs/superpowers/runbooks/cloud-lease-identity.md).

## Cloud Deployment offline bundle

`cloud-deployment-bundle.yml` runs before any Cloud Lease Quote or Acquire and
has only repository read permission. Its `request_id` is correlation-only and
does not change bundle content. It separates the trusted Workflow control
SHA from an immutable product SHA reachable from `main`, builds both frontend
bundles and all Linux AMD64 binaries on the runner, verifies checksum-pinned
native dependencies, and publishes a content-addressed Ubuntu 24.04 payload.
No Lease identity, cloud credential, runtime secret, or host address enters the
bundle. See
[`docs/superpowers/runbooks/cloud-deployment-bundle.md`](../../docs/superpowers/runbooks/cloud-deployment-bundle.md).

## Cloud Deployment activation

`cloud-deployment-activate.yml` has repository and Artifact read permission but
no OIDC or provider credential. It authenticates both caller-selected Artifact
runs as successful executions of the exact protected workflows on `main`,
builds trusted local validators before executing bundle code,
validates an active Lease Receipt, derives a WuKongIM Deployment Plan, transfers
one verified bundle through the public load node to three private service
nodes, activates native non-restarting infrastructure units while leaving the
stage-specific coordinator dormant, and emits either a typed Deployment
Receipt or bounded stable failure.
The two upstream runs need not share the Deployment Action's head SHA: long
bundle builds remain valid while `main` advances. The Lease provenance still
binds the immutable source and bundle digest, and the sealed bundle control SHA
must equal its authenticated producer run.
Only the top-level orchestrator may decide to Release or acquire a fresh Lease.
See
[`docs/superpowers/runbooks/cloud-deployment-activate.md`](../../docs/superpowers/runbooks/cloud-deployment-activate.md).

## Chat lifecycle full-scale rehearsal

`chat-lifecycle-rehearsal.yml` is the paid top-level Agent Tool. Invoke it only
after the user gives the exact paid-run authorization and required cloud
identity/deployment credentials are configured. Its complete operator surface
is exactly `source_sha`, fixed operator `tangtaoit`, one request-scoped Codex
diagnostic Ed25519 public key, and `request_id`; infrastructure, budget,
duration, workload, and retry values come only from the reviewed repository Run
Plan. It builds the immutable bundle, obtains a read-only Quote, acquires one
six-hour Lease by consuming that exact admitted Quote, activates the deployment, and starts the dormant rehearsal
unit. The runner exits after the remote `run-start.json` proves 10,000 full
syncs and acceptance of the first full 2,000 SEND/s grant. The remote rehearsal
uses the same sealed five-second accrued-cost and one-hour expiry-reserve guard
as formal execution, with a two-hour active-duration admission requirement.

Only deployment/readiness failure may trigger the one fresh Lease retry. The
orchestrator first releases the complete failed Lease to exact zero inventory,
rounds actual held host hours upward, reserves the full quoted egress and
retention risk when live traffic evidence is unavailable, carries that
conservative accrued cost into the shared CNY 1,350 operational stop, and excludes
that exact zone/compute-type pair. Runtime or correctness failure is never
retried. `chat-lifecycle-rehearsal-finalize.yml` discovers handed-off runs,
uploads a terminal report or bounded failure diagnostics, and only then invokes
Release until `zero_inventory == true`; a cleanup Artifact is the terminal
proof. The two-hour result can be `rehearsal_pass`, never formal `pass`.
The scenario-wide concurrency lock permits only one paid Chat Lifecycle
orchestrator at a time, and startup also refuses procurement while any prior
authenticated rehearsal or formal handoff or cleanup-pending Lease lacks its selector-bound zero
proof. A handed-off Lease is released immediately if Artifact
publication fails. Every immediate cleanup owner performs one complete
provider-bounded Release pass; the finalizer retries on its next schedule, and
the independent 15-minute Cloud Lease Sweep reconciles cleanup-pending or
expired inventory even if an owning job is canceled.

A passing rehearsal finalizer releases the rehearsal Lease first, authenticates
its selector-bound zero proof, and publishes one `formal_transition/v1` bound to
the same source SHA, bundle digest, request, and aggregate budget ledger. That
ledger uses exact Lease creation-to-zero-inventory time, observed non-loopback
transmit bytes rounded upward to GiB, and the full retention-risk allowance;
it does not commit the whole six-hour rehearsal Quote.
`chat-lifecycle-formal.yml` is an internal scheduled safety continuation, not a
second public operator surface. It consumes at most one unspent transition,
refuses procurement if either stage still has active inventory, reuses the
exact original bundle, and acquires a completely fresh 96-hour formal Lease.
Remote `wkbench-formal.service` owns the uninterrupted 72-hour Soak, hour-24
qualification, at-most-eight-hour aged-data capacity staircase, and 30-minute
2,000-SEND/s recovery in one `wkbench formal-chain` process with the same
worker fence, generation, observer, lifecycle proof, and dataset. Activation
seals the Lease creation/expiry instants, exact quote line items, aggregate
committed cost, and ¥1,350/¥1,500 limits into the root-only load environment.
Formal-chain checks the 81.5-hour admission reserve, then re-evaluates
conservative accrued host/retention/traffic cost and the one-hour expiry cleanup
reserve every five seconds through both phases. `chat-lifecycle-formal-finalize.yml` waits for
that process to exit, requires complete JSON/Markdown evidence pairs, and
uploads the terminal chain result or bounded
diagnostics, and only then releases the exact Lease to zero inventory. Failed
Release attempts persist a stage-authenticated cleanup continuation for the
next scheduled pass; the generic Cloud Lease sweeper remains the independent
expiry backstop.

## Workflow maintenance

- Keep external Actions pinned by full commit SHA.
- Keep candidate checkouts credential-free and reject tracked-tree mutation.
- Never expose App keys to candidate or model jobs.
- Update policy, schemas, Workflows, docs, and their contract tests together.
- Read this file before invoking or changing any Workflow.

Run:

```bash
GOWORK=off go test ./scripts/... -run 'Workflow|ReviewAgent' -count=1
node --test .github/review-agent/responses-budget-proxy.test.mjs
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.9 \
  .github/workflows/*.yml
```
