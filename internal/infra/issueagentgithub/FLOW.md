# Issue Agent GitHub Adapter Flow

`internal/infra/issueagentgithub` adapts narrow Issue Agent ports to GitHub's
REST and GraphQL APIs. It owns repository-scoped GitHub App installation-token
minting, bounded reads, complete signed-checkpoint history verification,
Artifact validation, and fenced publication. Business transitions remain in
`internal/usecase/issueagent`.

```text
read-only GitHub facts
  -> strict bounded DTO decoding
  -> usecase reconciliation plan

Publisher Environment Secret
  -> short-lived installation token
  -> re-read exact checkpoint, branch, PR, run, and Artifact identities
  -> validate typed ChangeSet and protected paths
  -> GraphQL createCommitOnBranch with expected branch head
  -> re-read Git ref, commit verification, and exact tree
  -> signed append-only checkpoint and deterministic Issue/PR projections
```

The adapter never executes target code, Worker files, Artifact executables, or
model-authored shell. Every write names one exact repository, Issue, generation,
sequence, predecessor, Agent branch, and expected old ref. Default branches,
tags, non-Agent branches, protected paths, force updates, unverified commits,
and stale plans fail closed.

Worker file proposals remain typed `ChangeSet` values. The Publisher validates
their paths, regular-file modes, frozen reproduction files, byte budgets, and
scenario instruction template before publication. It creates or reuses only
`agent/issue-<number>`, uses `expectedHeadOid` to prevent branch races, requests
one single-parent GraphQL commit, requires GitHub's verified signature, and
re-reads the ref and complete content. A partially created reproduction branch
may be reused only when it still equals the exact expected parent.

Issue comments, exact label sets, Draft PR state, validation requests, and
tracking Issues are projections of already-validated state. No method can
merge a PR, close a Bug Issue, force-update a ref, or write a default branch or
tag. A saturated inventory, paginated history, unknown response shape, corrupt
checkpoint, stale lease, or object mismatch fails closed.

An admin recovery is the only exception to ordinary contiguous history
verification. A later valid signed recovery checkpoint must identify the exact
last valid anchor, every intervening App marker, and a digest of those raw
comments; only that enumerated segment is quarantined. Unresolved review
threads are read through one bounded GraphQL page and frozen into the signed
review task.
