# Agent Validation Status Cleanup Design

## Problem

The Agent PR validation protocol intentionally fails closed after a pull
request is opened, edited, reopened, or synchronized. Two independent signals
currently express that state:

1. the first attempt of the generation-bound `Agent Validation Gate` check
   fails; and
2. `agent-pr-validation-control.yml` publishes the unscoped classic commit
   status `Agent Validation Request / PR #<number>` as `failure`.

Terminal validation publishes success to different, generation-bound contexts:

- `Agent Validation Request / PR #<number> / Gate #<run-id>`; and
- `Agent Validation Evidence / PR #<number> / Gate #<run-id>`.

Because GitHub groups classic commit statuses by their complete context string,
the terminal statuses never replace the unscoped invalidation. Every validated
head therefore retains a visible failure even when its selected suites,
evidence, and required `Agent Validation Gate` all pass.

## Goals

- Preserve the fail-closed merge protocol and exact gate-generation binding.
- Preserve cancellation of a running same-PR validation worker through the
  repository-wide PR-numbered concurrency group.
- Preserve the PR-event invalidation audit summary.
- Stop publishing a classic commit status that cannot reach a terminal success
  state.
- Reduce the invalidation job to the minimum permissions required for its
  remaining behavior.
- Lock the status-context boundary into the static Workflow contract tests.

## Non-goals

- Do not change suite selection, validation-plan syntax, retry rules, fork
  permissions, or branch protection.
- Do not weaken the first-attempt failure of `Agent Validation Gate`.
- Do not rewrite existing commit statuses on historical pull-request heads.
- Do not trigger or rerun GitHub Actions as part of this repository change.

## Options Considered

### 1. Remove the unscoped invalidation status

Keep the invalidation job and its PR-numbered concurrency group, but remove the
status API write. The job continues to cancel a running same-PR validation
worker and records a step summary. The generation-bound gate remains the sole
fail-closed merge signal.

This is the selected option. It removes the misleading terminal state without
introducing a second source of truth.

### 2. Overwrite the unscoped status after terminal validation

The worker could publish `success` to the same unscoped context. This preserves
the current invalidation marker, but a superseded worker can race with a newer
PR event and cosmetically overwrite the newer invalidation. The required gate
would remain safe, but the display state could still be wrong.

This option is rejected because it retains an unnecessary, non-generation-bound
status and adds a cross-generation race.

### 3. Make the invalidation status generation-bound

The invalidation job could try to discover the matching merge-gate run ID and
publish the same context used by the request. The control and merge-gate
Workflows start independently on the same event, so discovery introduces
ordering and polling complexity. The merge gate already expresses the exact
generation state.

This option is rejected as redundant and more complex.

## Selected Design

`agent-pr-validation-control.yml` keeps the `invalidate` job condition and
concurrency group. The job no longer calls the commit-status API and no longer
requests `statuses: write`. It writes only the existing summary explaining
that the PR event invalidated previous validation and that a fresh plan is
required. Its job-level concurrency key intentionally matches the validation
worker's workflow-level key, so a new PR event cancels a running same-PR worker.
An Agent must wait for the invalidation job to finish before triggering the
fresh request; canceled or superseded workers cannot satisfy a newer gate.

The authoritative state flow becomes:

1. A PR event creates a new `Agent Validation Gate` generation.
2. Its first attempt fails closed.
3. The invalidation control job cancels a running same-PR validation worker and
   records its summary without publishing a commit status.
4. After that job completes, an authorized Agent publishes a plan and triggers
   the request.
5. The worker publishes generation-bound request and evidence statuses.
6. The worker reruns the original gate, which verifies the exact generation and
   becomes the only branch-protection verdict.

The change does not alter any code checked out from a pull request, any secret
boundary, or any remote execution capability.

## Test Strategy

The Workflow contract test must first fail against the current implementation
by enforcing all of these properties:

- the invalidation job remains present with its existing trigger condition and
  concurrency group;
- the invalidation job has no write permission;
- it contains exactly one script-only summary step with no action, environment,
  inputs, condition, or alternate command;
- mutations that add another action or a differently constructed status API
  write are rejected;
- generation-bound request and evidence contexts remain required in the
  request, worker, and merge-gate contracts.

After the Workflow change, run the focused Agent validation contract tests,
parse every Workflow as YAML, and run the full default `scripts` unit package.

## Documentation

Update `.github/workflows/README.md` and `docs/development/CI.md` so they name
the generation-bound gate as the invalidation signal and describe the control
job as same-PR worker cancellation plus audit summary, not a PR-numbered
commit-status writer.

This behavior is operationally important and should also be recorded concisely
in `docs/development/PROJECT_KNOWLEDGE.md`.

## Acceptance Criteria

- A newly opened, edited, reopened, or synchronized PR still starts with a
  failing `Agent Validation Gate`.
- A successful validation can leave no unscoped Agent validation failure
  context on a new PR head.
- A PR-event invalidation still cancels a running same-PR validation worker.
- Superseded request and worker runs remain unable to satisfy a newer gate
  generation.
- Branch protection continues to require only `Agent Validation Gate` from the
  GitHub Actions app.
- All Workflow contract and default `scripts` unit tests pass.
