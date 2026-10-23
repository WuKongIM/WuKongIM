## Summary / 变更摘要

When chat-lifecycle finalization reports `metrics_fetch` with kind `parse`, expose a closed parser rejection reason at the same declared service-source position. Twenty fixed guard families use private typed causes and retain their original error text. The optional `parse_reasons` vector has exactly three positions and never retains metric lines, labels, endpoint bodies or arbitrary error strings.

Partial records and repeated outer `metrics_fetch` objects preserve the default JSON behavior of the existing source mask and kind array. For example, a second object containing only malformed parser reasons retains the earlier valid mask `5` and kinds instead of clearing the diagnostic. Omitted/null legacy fields, explicit zeros, null array cells and short arrays keep their prior semantics; the optional vector is independently absent or discarded when invalid. Whole invalid JSON and old field type errors remain rejected.

Acquisition, fallback, parser guards and limits, source joining, parent cancellation, settle/accounting, report validity and CLI outcomes remain unchanged. This adds bounded observability for the next diagnosis. It does not reconstruct the missing failed scrape or fix the retained presence timeout/message loss. The candidate remains **NO-GO for v3 GA** until the outstanding product failure and required acceptance evidence are resolved. This PR starts no workload or cloud resources.

## Release notes / 发布说明

- [x] I added every user-visible change to `CHANGELOG.md` under `[Unreleased]`.
- [ ] This pull request has no user-visible change. I explained why below and a maintainer added the `skip-changelog` label.

## Verification / 验证

- The original 52 failure/control matrix and seven test files were frozen before implementation and retain their hashes. Three separately frozen null-element tests cover each vector position.
- Independent review found partial-record compatibility missing. A separate supplement freezes 38 old-DTO differential/control cases: 18 reused receivers, 14 repeated outer objects, and six old-field/whole-JSON rejection controls. They reproduced the earlier implementation's field reset before the one-line seed correction, then passed with unchanged hashes.
- Normal suite after the compatibility correction: target, chat-lifecycle and all `cmd/wkcli/...` packages passed (43.416 seconds).
- Race suite after the compatibility correction: target, chat-lifecycle and wkcli benchmark packages passed (118.276 seconds).
- Policy-named `go-format` and `flow-doc-contracts` passed; the existing canonical FLOW index remains valid.
- Normalizing only the 30 fixed parser constructors reproduces the complete original client byte for byte. The compatibility correction adds only the old-field seed and explanatory comments; it changes no parser, collector, HTTP, resource or verdict behavior.

The earlier head's unfinished CI generation is retained separately and is not reused as acceptance. No manual CI dispatch, re-enable or rerun, runtime workload, paid cloud operation or database query was performed.
