# Send-ban documentation contract repair

Approved scope: separately repair the existing documentation drift for permission RPC 91 and four send-ban GET/POST routes, using source `424d03eb298b972ec572261d617972eecb5523c5`. Preserve exact catalog/registration parity rather than removing assertions. No runtime business changes, Workflow/policy changes, deployment or release publication.

Failure-first tests: all 48 Product HTTP entries and all 61 shared transport IDs; strict 0/1 flags, unknown-field rejection, nullable/omitted CAS, decimal uint64 boundaries, response shape and legacy omitted/null policy preservation. Both original parity checks and new schema checks failed before contract changes.

Inspect API/usecase/adapter/storage authority, missing-entity behavior and apply-time version semantics; publish typed failures, caller trust, byte/syntax limits and uncertain-write recovery in both languages. Generate pages from the complete OpenAPI contract and run its named checks. The Go transport uniqueness fixture also needs RPC 91, without changing IDs or runtime code.
