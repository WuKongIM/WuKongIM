# Spec review

Read-only two-axis skill review against base
`eb4c70b270193b22a520b8fe06b44ec72b19f333`, including working-copy and new files.
Specification: `docs/specs/mqtt-storage-partition.md` and the approved user scope.
No writes or runtime tests were performed by the reviewer.

Final source verdict: **no remaining actionable Spec findings**.

The initial timing finding was closed: reserved gauges are updated before
commit, so the unknown slice now requires a separate successful post-commit
witness while links remain cut. The witness is restricted to positive charges,
and exactly one increment from its recorded starting count is required before
healing. Zero-byte maintenance cannot satisfy it. Both scenarios also verify
reopened-message completion returns every node to zero debt.

The relay routes static membership through separate owned endpoints, closes
and refuses both directions, preserves node-local/public TCP and joins copy
workers. The accepted-data scenario checks surviving quorum, retained debt,
persistent PacketID/DUP/native identity, independent ACKs, retirement and reopening.
Final runtime qualification belongs to the manifest, not this source review.
