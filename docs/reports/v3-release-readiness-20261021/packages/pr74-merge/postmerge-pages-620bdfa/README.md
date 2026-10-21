# PR #74 — first post-merge Pages verification

Natural push run 39626500979, attempt 1, succeeded for the merged commit 620bdfa1804c732dc1b3b072bb64dbe6f0ae0475. The raw checkout log names that exact commit; its tree 7a498c42b4f5b0e6f86a524878fde651d7301371 is the same reviewed and native-tested tree.

The raw log records 528 Python tests with zero skips and four publication-control tests, all passing. Configure Pages, artifact upload, and deployment were skipped. The exact merged manifests/channels.json has stable.enabled=false, status=object_storage_required and releases=[]. Its Git blob digest and decoded public bytes are checked by verify-receipts.py.

This observer only read source/GitHub state and verified saved receipts. No retry, dispatch, cancellation, source/PR change or cloud action was performed. No post-merge native job was dispatched by this observer. The 49 native cases were already accepted on the same tree in generation 6; this small archive concerns only the natural post-merge Pages run. It does not establish production publication.

Run python3 verify-receipts.py to reproduce the offline assertions summarized in summary.json. This directory is isolated from the original pr74-merge root receipts and does not overwrite them. Public tool diagnostics redact URL queries and tokens; any raw stderr stays in the sibling private observer directory outside this archive. The allowlist, SHA256 and CRC32 files define all public payloads.
