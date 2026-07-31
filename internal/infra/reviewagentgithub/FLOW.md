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
  -> expected-head append only

Review Agent App token
  -> fresh generation/governance fences
  -> one mutable status comment
  -> one formal Review with bounded inline comments
  -> one Review Agent Verdict Check Run
```

The Review App adapter exposes no contents, branch, commit, merge, close,
dismiss, resolve, Ruleset, Actions, or Secrets operation. The State Writer
adapter accepts no caller-selected ref or path and exposes no Review, comment,
Check, merge, or pull-request mutation.
