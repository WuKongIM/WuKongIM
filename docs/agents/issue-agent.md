# GitHub Issue Agent

The Issue Agent turns an authorized, reproducible Bug Issue into a Draft pull
request. It runs entirely on GitHub-hosted Actions runners; the GitHub App is a
repository-scoped identity and token issuer, not a deployed server.

## Intake

The Bug Issue Form intentionally asks for only four required facts:

1. exact affected version;
2. environment, cluster topology, and client version;
3. reproduction steps;
4. expected and actual result.

Frequency and redacted logs or configuration are optional. Intake is
deterministic: it validates the form, applies `needs-triage` or `needs-info`,
and may suggest possible duplicates. It does not execute Issue text, call a
model, resolve a version, create a branch, or close the Issue.

Accepted version syntax is an exact semantic release tag, a full 40-character
commit SHA, or an image reference pinned by a SHA-256 digest. Moving references
such as `latest` are rejected.

## Authorization and state

Execution begins only when a maintainer with write, maintain, or admin
permission adds `ready-for-agent`. Signed, append-only checkpoints embedded in
Issue comments are the sole workflow state authority. Events are only wake-up
hints; every run re-reads and verifies current GitHub state.

The normal flow is:

```text
intake -> authorize -> pin versions -> reproduce -> Draft PR
       -> diagnose -> fix -> validate -> Ready for Review
```

The Agent never merges a PR or closes the Issue.

## Rollout

The protected policy supports `disabled`, `shadow`, `intake`, `reproduction`,
`remediation`, and `general`. Each mode is a capability ceiling. Move one stage
at a time only after reviewing audit output and failure modes from the previous
stage.

Enabling write modes requires the repository-scoped GitHub App, Publisher
Environment, checkpoint signing key, and matching reviewed public key. Private
keys must never be printed or uploaded as artifacts.
