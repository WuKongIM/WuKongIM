## Summary / 变更摘要

When chat-lifecycle finalization reports `metrics_fetch` with kind `parse`, expose a closed parser rejection reason at the same declared service-source position. Twenty fixed guard families use private typed causes and keep their original error text; the optional `parse_reasons` vector contains exactly three positions and never retains metric lines, labels, endpoint bodies or arbitrary error strings.

Older reports keep this field absent. Wrong-type, oversized and invalid optional vectors are discarded independently; invalid JSON and existing mask/kind field errors remain rejected. Acquisition, fallback, parser guards and limits, joining, parent cancellation, settle/accounting, report validity and CLI outcomes remain unchanged.

This is bounded observability for the next diagnosis. It does not identify the original failed scrape or repair the retained presence timeout/message loss. The current candidate remains **NO-GO for v3 GA** until the outstanding product failure and required acceptance evidence are resolved; this PR starts no workload or cloud resources.

## Release notes / 发布说明

- [x] I added every user-visible change to `CHANGELOG.md` under `[Unreleased]`.
- [ ] This pull request has no user-visible change. I explained why below and a maintainer added the `skip-changelog` label.

## Verification / 验证

- The approved 52 failure/control matrix and seven test-file hashes were frozen before production changes. The first compile-only RED and a malformed minimal-shell attempt were retained separately; a complete compilable behavior RED preceded the first GREEN with unchanged test hashes.
- Independent WIP review found JSON `null` elements accepted by `Decode(&string)`. Three separately frozen cases, one per vector position, failed before the decoder correction and passed afterward; the original seven files stayed byte-identical.
- Whole `target/client.go` is byte-identical to base after normalizing only 30 fixed error constructors back to their original text. The complete metadata collector is likewise unchanged after removing the fixed parser-reason projection.
- Final normal suite: target, chat-lifecycle and all `cmd/wkcli/...` packages passed.
- Final race suite: target, chat-lifecycle and wkcli benchmark packages passed (127.319 seconds).
- Policy-named `go-format` and `flow-doc-contracts` passed; canonical FLOW index regenerated. Existing FLOW length warnings remain.

No runtime run, paid cloud operation, database query, manual CI dispatch or automatic retry was performed.
