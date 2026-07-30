# Issue Agent Verifier Flow

`internal/runtime/issueagentverify` owns credential-free candidate capture and
clean-checkout verification. It contains no GitHub API, model, Issue lifecycle,
or Publisher logic.

```text
immutable baseline + disposable Codex workspace
  -> filesystem comparison that ignores Codex Git metadata
  -> canonical bounded CandidateSnapshot

fresh exact-base checkout + CandidateSnapshot + protected policy
  -> apply complete regular-file ChangeSet
  -> recompute the complete diff
  -> protected-path, mode, dependency, size, and risk checks
  -> trusted fixed and focused test plan
  -> CandidateEvidence
```

Codex-authored Git refs, index state, attributes, ignore rules, command claims,
and result fields are never evidence. Existing safe repository symlinks must
remain byte-for-byte unchanged; added, removed, retargeted, escaping, chained,
or file-type-changing symlinks fail closed. Only regular-file upserts and
deletions may enter a candidate. Every `AGENTS.md` and `FLOW.md` is protected
because its exact source blob identity is part of the trusted task context.

The Verifier runs candidate code without a model or Publisher credential.
Docker-dependent fixed scenarios belong to a separate trusted job; the
Engineer never receives the host Docker socket.
