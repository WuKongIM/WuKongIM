# Review Agent GitHub Adapter Flow

`internal/infra/reviewagentgithub` is the only GitHub boundary for Review
Agent facts, signed state refs, and GitHub projections.

```text
zero-authority event hint
  -> fresh PR/files/reviews/threads/comments/checks metadata
  -> one exact base/head content read for the dispatched review Context
  -> usecase facts and verification inventory

Review State Writer App token
  -> exact review-state/pr-N or review-state/scheduler ref
  -> verified latest + immediate predecessor rolling checkpoint
     (older commits remain append-only audit history)
  -> one legacy scheduler checkpoint that only repeats its canonical
     predecessor with empty JSON collections may be loaded for the next append
  -> expected-head append only

Review Agent App token
  -> App JWT verifies the protected policy App ID and slug before minting
     one exact repository-scoped installation token
  -> fresh generation and human-review fences
  -> one mutable status comment
  -> one formal Review with bounded inline comments
  -> one Review Agent Verdict Check Run
  -> one exact-head merge only after fresh admin/member authorization
```

The bounded scheduler recovery never rewinds or rewrites the protected state
ref. A strict successor may name that one legacy checkpoint; after the next
successor, it leaves the two-checkpoint verification window.

After a signed GraphQL commit, the State Writer tolerates bounded ref
read-your-write lag only while GitHub still reports the exact expected parent.
The committed head must become visible within the retry budget; any third head
is real contention and fails immediately.

The Review App token includes GitHub's required `contents:write` permission for
the pull-request merge endpoint, but the adapter exposes only an exact-head
normal merge after the pure authorization plan succeeds. It exposes no generic
contents, branch, commit, close, dismiss, resolve, Ruleset, Actions, or Secrets
operation. The State Writer adapter accepts no caller-selected ref or path and
exposes no Review, comment, Check, merge, or pull-request mutation.
