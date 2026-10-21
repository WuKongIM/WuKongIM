# Issue preflight failure inventory (before implementation)

Scope authorized by root: only comment-request-issue.sh and one dedicated integration test. Public seam: real bash CLI with external gh stub; no cloud/GitHub writes. Existing no-flag comment/deduplication/close behavior remains. No applicable FLOW applies to scripts/chat-lifecycle; rule/source SHA frozen separately. Root owns caller wiring, CHANGELOG and docs.

1. Missing mandatory environment or unknown/repeated flags must fail before GitHub reads/writes.
2. Current script demands ISSUE_STATE even in requested preflight; valid minimal environment must instead produce one typed ready identity.
3. Empty or fuzzy-only search must reject exact tracking Issue missing. Exact-title PR must not qualify.
4. Duplicate exact-title Issues, including closed duplicates, must reject; never choose first/newest.
5. incomplete_results, search truncation (total_count > returned count), malformed envelope/count/items must reject; no pagination/retry can silently hide ambiguity.
6. Invalid or foreign-repository Issue identity, missing/invalid number/id/title/URLs/state/comments must reject.
7. Search/API error or exact GET failure must yield typed failure, no comment/close/create.
8. Exact GET must independently reject state closed, mismatched identity/repository/title, PR, malformed Issue, and locked Issue; stale search cannot qualify current wrong identity.
9. Malicious inherited ISSUE_CLOSE/body/state must be irrelevant to preflight; every API call must use GET only and exact fixed GitHub/repo endpoints.
10. Existing no-flag comment, dedupe, and close regression must still pass.

Success JSON freezes only the observed open Issue identity. It cannot prove future write permission or prevent later closure/transfer. No new resource/approval abstractions are introduced.
