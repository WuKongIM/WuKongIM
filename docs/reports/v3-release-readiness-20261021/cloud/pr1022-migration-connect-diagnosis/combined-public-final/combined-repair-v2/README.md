# Migration readiness and recovery-barrier repair — local evidence

Frozen source: base `9a4900dc67ac674791edba11ea9a719cd91ec570` plus the seven-file patch `38519d247817780b633401280ffebba3433bf0f8b0bf5ee1e6bbc059a38c03b0`. The dedicated recovery-barrier worktree remains uncommitted; shipping9a is untouched. `source-freeze.json` identifies all seven bytes, and `post-test-source-check.json` verifies they remained fixed through testing.

## Behavior

The already-reviewed fixture waits for node1 WKProto only after cold imported-history checks and before retained-token authentication. Its new diagnostics are evaluated only on failure. Business Connect/SEND assertions, original deadlines and the existing exact HTTP503/unavailable retry contract are unchanged.

Install now retains ErrNotReady identity only for a locally durable recovery barrier whose entire missing-voter set supplied valid current-round NeedFrom receipts within the barrier bound. Quorum failure identity is retained. Unknown, conflict, cancellation, local nondurability and Backpressured remain closed. This does not change general wire mappings or schedule follower repair.

## Results

- Formal 19-case test was written first: two NeedFrom-positive cases failed before the 13-line product change; all 19 passed afterward. Full replication package passed once (224 pass events including package, no failures/skips; 1.09 s package / 1.799 s wrapper).
- FLOW named gate first rejected 152 lines; after two-line compression it rejected stale generated index. These are separate retained failures. Regenerating the one index row (148→150) yielded flow-doc-contracts PASS; go-format PASS. Limits and policy commands were unchanged.
- Built one e2e binary from the frozen combined source (vcs.modified=true), SHA256 `8543864c52d254b50b1a585b9c4db4784defc5d5f14b8cf2fb05f51db9a751b3`.
- Focused 1→3 first run PASS: 13 complete history checks, 0 HTTP503 retries, 29.183 s wrapper.
- Full five topologies first run PASS: 55 complete history checks (4/13/4/13/21), 0 HTTP503 retries, 104.731 s package / 106.079 s wrapper. No failed/skipped case. Both execution sessions ended and cleanup verified zero remaining owned processes.

## Preserved limits and history

Original Linux TCP refusal remains unexplained; three bounded local observation runs did not reproduce it. The initial fixture-v1 full run separately failed 1→3 after restart with HTTP400 durable-quorum-unavailable. Its historical per-voter cause was not recorded and remains unknown. The five-arm diagnostic demonstrates cause collapse under injected outcomes, not that original fault. Original frozen evidence is retained unmodified.

The final E2E passed without exercising a 503 retry, so it does not prove that a real lagging follower hit the new classification or caught up because of this change. Synthetic tests prove the narrow classification and unchanged frontier/history safety, with controlled fault removal. Linux full CI on the delivered head remains necessary. No cloud action, retry of CI, commit, push or merge was performed.

The archive root `ARCHIVE_ALLOWLIST.json` lists the finite public evidence set with exact source and archive hashes. The binary, DBs, caches and private material are excluded. Prior manifests remain untouched; FLOW source is represented by patch/digest, not an active FLOW.md copy.
