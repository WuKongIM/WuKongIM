# PR #70 merge and postmerge proof

As of 2026-10-21 08:40Z, the approved public-only registration head `0c26496b624058db813aa1206a7910f0abae6b13` was rebase-merged to `51bfedbe88c78758fa05bfb87b13541b4bca4796`. The operator merge result was observed at 08:34:33Z. Exact local commit objects have the same full tree `2c4f7d3a9b24a6f67c18b114b8a5c5f1f5f6fa5e`.

[Postmerge run 39570313491](https://github.com/WuKongIM/packages/actions/runs/39570313491), attempt 1, succeeded at 08:36:26Z. The job log proves checkout of the exact merged SHA, 400 Python tests (102.131 seconds) and 3 workflow contract tests. Configure Pages, artifact upload and deployment were skipped. This was public certificate registration validation; it did not publish stable packages or enable the channel.

`source-tree-and-cleanup.json` freezes the applicable control/rule hashes and clean worktree identity. Rebase produced a different commit identity; the original task head is not an ancestor of the merged commit. The clean original branch and worktree are preserved, with no forced deletion.

The first PR metadata read returned EOF and left `merged-pr.json` empty. One bounded metadata read retry also returned EOF; its stdout, stderr and result are preserved. These were GET requests, not workflow reruns. The successful merge response, fetched commit/main ref and successful exact-SHA postmerge run remain independent evidence; no exact `merged_at` API timestamp is invented.

This directory contains public metadata and small logs only. No secret material, Keychain access, NAS reads, cloud resource actions or publication occurred in this verification. `ARCHIVE_ALLOWLIST.txt` lists every archival file, and `SHA256SUMS` covers every listed payload plus the allowlist itself (excluding the checksum file to avoid a self-reference).
