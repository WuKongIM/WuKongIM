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
maps to failure, and `inconclusive` or missing owner approval maps to
`action_required`.

The signed lease bounds the complete generation to 90 minutes. Infrastructure
failure is retried once inside that same generation and deadline; a late result
is forced to `inconclusive`. A merge conflict bypasses the model and publishes
`changes_required`. Candidate baseline commands run only after the shared
network fence disables both Docker access and `sudo`. Candidate checks receive
isolated loopback inside a rootless network namespace. The pinned Action proxy
is the only runner-loopback transport exception and the model permission
profile still denies model-initiated localhost; private networks stay
unreachable.

Missing Context, reviewer, or trusted-baseline artifacts are evidence of an
infrastructure failure, not reasons to abort the state machine. The Evidence
job records the bounded retry or terminal `inconclusive` completion so signed
state and the repository queue always advance.

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
- Keep candidate checkouts read-only with `persist-credentials: false`.
- Never expose App keys to candidate or model jobs.
- Update policy, schemas, Workflows, docs, and their contract tests together.
- Read this file before invoking or changing any Workflow.

Run:

```bash
GOWORK=off go test ./scripts/... -run 'Workflow|ReviewAgent' -count=1
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.9 \
  .github/workflows/*.yml
```
