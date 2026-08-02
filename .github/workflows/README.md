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
| `review-agent-pr-signal.yml` | `Safety Automation - Review Agent PR Signal` | Emits a credential-free PR/Review/comment wake-up hint |
| `review-agent-issue-signal.yml` | `Safety Automation - Review Agent Issue Signal` | Wakes open PRs whose intent links to an edited, closed, or reopened Issue |
| `review-agent.yml` | `Safety Automation - Review Agent Controller` | Re-reads GitHub facts and signed state, then plans one lifecycle transition |
| `review-agent-run.yml` | `Agent Tool - Review Pull Request` | Runs one exact review or explanation generation |
| `issue-agent-pr-signal.yml` | `Safety Automation - Issue Agent PR Signal` | Emits credential-free lifecycle and Review hints for Issue Agent PRs |
| `issue-agent.yml` | `Safety Automation - GitHub Issue Agent` | Reconciles Issue work and Review Agent repair requests |
| `issue-agent-engineer.yml` | `Agent Tool - Issue Engineer` | Runs one exact Context Builder, Codex Engineer, and clean Verifier chain |
| `cloud-sim-provision.yml` | `Agent Tool - Provision Cloud Simulation` | Creates a leased Alibaba Cloud Simulation Run |
| `cloud-sim-analyze.yml` | `Agent Tool - Analyze Cloud Simulation` | Operates one bounded cloud analysis session |
| `cloud-sim-oidc-subject.yml` | `Agent Tool - Configure Cloud Simulation OIDC Subject` | Configures and verifies the cloud OIDC subject |
| `cloud-sim-cleanup.yml` | `Safety Automation - Reconcile Cloud Simulation Resources` | Destroys expired cloud leases and supports exact cleanup |
| `cloud-sim-monitor.yml` | `Safety Automation - Patrol Cloud Simulation Runs` | Patrols retained live runs and records bounded health evidence |

## Review Agent

Every open, ready pull request targeting `main`, including a Fork pull
request, enters the review-only Review Agent flow:

```text
PR/Review/comment event
  -> zero-permission Signal
  -> protected-default-branch Controller
  -> fresh GitHub facts + signed PR state + signed scheduler
  -> exact context + deterministic checks + one ephemeral model session
  -> evidence validation + signed terminal state
  -> status comment + formal Review + Review Agent Verdict
  -> exact-head auto-merge for a repository admin or organization member
     | otherwise wait for a human merge
```

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

Worker dispatch is serialized per pull request. The exact run title derived
from pull request, signed lease, and infrastructure attempt is the idempotency
key at both Controller and retry-drain boundaries, so concurrent recovery
cannot start the same attempt twice.

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

See [`docs/agents/issue-agent.md`](../../docs/agents/issue-agent.md).

## Cloud Simulation

Cloud creation and permission changes remain explicit Agent Tools. Cleanup and
live-run patrol remain the only scheduled safety automations. Provider
credentials, analysis credentials, and cleanup authority stay in their
documented separate Environments.

See
[`docs/superpowers/runbooks/cloud-simulation.md`](../../docs/superpowers/runbooks/cloud-simulation.md).

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
