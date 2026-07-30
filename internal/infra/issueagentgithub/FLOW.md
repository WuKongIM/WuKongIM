# Issue Agent GitHub Adapter Flow

`internal/infra/issueagentgithub` adapts narrow Issue Agent ports to bounded
GitHub REST and GraphQL calls.

```text
read token
  -> Issue, comments, permissions, unresolved Review threads
  -> exact-source recursive tree for AGENTS.md and FLOW.md blob identities
  -> credential-free ContextBundle

protected Publisher App credential
  -> repository-scoped short-lived installation token
  -> verify complete agent-state/issue-N signed commit ancestry
  -> re-read Issue authority, Agent ref, commit, and Draft PR
  -> expected-head GitHub-signed commit on agent/issue-N
  -> complete Draft PR + signed state successor + one status comment
```

The adapter never executes candidate code. Default branches, tags, non-Agent
refs, protected paths, stale heads, external commits, unsigned state, truncated
pagination, and ambiguous PRs fail closed. Candidate commits are accepted only
when their exact parent, message, paths, blob identities, configured App Bot
author, and GitHub signature match. No method merges a PR or closes a Bug
Issue.

The scheduled inventory is a complete, sorted set of at most 40 open
`ready-for-agent` Issues. A larger or paginated result fails closed.
