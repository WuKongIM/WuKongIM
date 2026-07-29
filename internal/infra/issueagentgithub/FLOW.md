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
  -> for moving-main recovery only, apply the independently computed exact
     main-to-tree delta on a staging ref through signed createCommitOnBranch
  -> atomically CAS the Agent ref and delete staging via updateRefs beforeOid
  -> signed append-only checkpoint and deterministic Issue/PR projections
```

Installation-token responses remain strict at the top level. GitHub returns a
complete repository object for each selected repository, so the adapter treats
that bounded metadata as opaque except for `id` and `full_name`. It still
requires `repository_selection=selected`, one exact repository, the reviewed
permission set, and a bounded future expiry before accepting the token.

The adapter never executes target code, Worker files, Artifact executables, or
model-authored shell. Every write names one exact repository, Issue, generation,
sequence, predecessor, Agent branch, and expected old ref. Default branches,
tags, non-Agent branches, protected paths, unverified model-authored commits,
and stale plans fail closed. The only non-fast-forward update is the one typed
mechanical-rebase effect: an atomic `updateRefs` transaction with the signed
prior Agent OID and exact staging OID.

Worker file proposals remain typed `ChangeSet` values. The Publisher validates
their paths, regular-file modes, frozen reproduction files, byte budgets, and
scenario instruction template before publication. It creates or reuses only
`agent/issue-<number>`, uses `expectedHeadOid` to prevent branch races, requests
one single-parent GraphQL commit, requires GitHub's verified signature, and
re-reads the ref and complete content. A branch that already existed at the
reproduction parent is a publication collision and is never adopted. A
reproduction commit created by an interrupted Publisher may be reused only
when its parent, deterministic message, complete content, configured App Bot
author, and GitHub signature all match the pending effect.
The separate mechanical-main effect accepts only a bounded trusted ChangeSet,
exact result tree, current-main parent, deterministic message, verified
signature, and exact configured App Bot author. It creates the commit on a
deterministic staging ref keyed by the complete immutable rebase effect, then
atomically swaps the PR ref and deletes staging with `updateRefs.beforeOid`.
An orphan from a failed expected-head race therefore cannot collide with a
later adopted-head plan. This preserves current main as the PR merge-base
and makes recovery content- and identity-exact even if a crash occurs between
commit creation, ref swap, and checkpoint publication.

Issue comments, exact label sets, Draft PR state, validation requests, and
tracking Issues are projections of already-validated state. No method can
merge a PR, close a Bug Issue, or write a default branch or tag. No method can
force-update a ref except the typed mechanical-rebase transaction above, which
must atomically match both managed refs by exact `beforeOid`. A saturated
inventory, paginated history, unknown security-relevant response shape, corrupt
checkpoint, stale lease, or object mismatch fails closed.
Completed Worker-run inventory is filtered to the signed lease time window and
bounded to GitHub's documented 1,000-result search ceiling. Reconciliation
accepts only a unique exact display-title and Artifact-name match, then the
ordinary Publisher revalidates the downloaded task and result against the
current signed lease.

An admin recovery is the only exception to ordinary contiguous history
verification. A later valid signed recovery checkpoint must identify the exact
last valid anchor, every intervening App marker, and a digest of those raw
comments; only that enumerated segment is quarantined. Unresolved review
threads are read through one bounded GraphQL page and frozen into the signed
review task.
