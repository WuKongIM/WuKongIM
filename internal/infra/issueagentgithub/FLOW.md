# Issue Agent GitHub Adapter Flow

`internal/infra/issueagentgithub` adapts narrow Issue Agent ports to GitHub's
REST and Git Database APIs. It owns GitHub App installation-token minting,
bounded reads, signed checkpoint comment parsing, Artifact retrieval, and
fenced publication. Business transitions remain in
`internal/usecase/issueagent`.

```text
read-only GitHub facts
  -> strict bounded DTO decoding
  -> usecase reconciliation plan

Publisher Environment Secret
  -> short-lived installation token
  -> re-read exact checkpoint, branch, PR, run, and Artifact identities
  -> validate typed ChangeSet and protected paths
  -> Git Database blobs/tree/commit/ref
  -> signed append-only checkpoint and deterministic Issue/PR projections
```

The adapter never executes target code, Worker files, Artifact executables, or
model-authored shell. Every write names one exact repository, Issue, generation,
sequence, predecessor, Agent branch, and expected old ref. Default branches,
tags, non-Agent branches, protected paths, force updates, unverified commits,
and stale plans fail closed.

