# Issue Agent GitHub Adapter Flow

`internal/infra/issueagentgithub` adapts narrow Issue Agent ports to bounded
GitHub REST and GraphQL calls.

```text
read token
  -> Issue, comments, permissions, unresolved Review threads
  -> exact-source recursive tree for mandatory AGENTS.md and advisory FLOW.md identities
  -> credential-free ContextBundle

protected Publisher App credential
  -> repository-scoped short-lived installation token
  -> verify complete agent-state/issue-N signed commit ancestry
  -> re-read Issue authority, Agent ref, commit, and Draft PR
  -> expected-head GitHub-signed commit on agent/issue-N
  -> complete Draft PR + signed state successor + one status comment

stale Ready Agent PR + current main
  -> verify bounded linear App-signed history from signed source
  -> reconstruct its exact aggregate ChangeSet
  -> prove candidate paths are unchanged on current main
  -> build and verify exact result tree
  -> App-signed staging commit + atomic main/Agent/staging ref fences
  -> signed state successor + fresh Review generation
```

The adapter never executes candidate code. Default branches, tags, non-Agent
refs, protected paths, stale heads, external commits, unsigned state, truncated
pagination, and ambiguous PRs fail closed. Candidate commits are accepted only
when their exact parent, message, paths, blob identities, configured App Bot
author, and GitHub signature match. No method merges a PR or closes a Bug
Issue.

Mechanical base synchronization never invokes candidate code or resolves a
semantic conflict. It accepts only the signed state's exact bounded candidate
history, refuses external heads and candidate-path overlap, and uses a bounded
staging ref so a failed compare-and-swap cannot silently replace the Agent
branch. An exact App-signed ref swap interrupted before its state write is
recoverable even if `main` advanced again: its exact published base is recorded
first, then a later reconciliation continues forward. Every other head
mismatch fails closed.

If a state publication reports an error or an untrusted immediate result after
the remote write, the State Store re-reads the per-Issue ref through one
context-cancellable, 3.1-second bounded consistency window. It recovers only
when the ref points to the exact expected parent, canonical state content,
path, configured App Bot author, and GitHub-signed commit. Missing, different,
or still-unverifiable successors retain the original fail-closed result.

The scheduled inventory is a complete, sorted set of at most 40 open
`ready-for-agent` Issues. A larger or paginated result fails closed.
