# RPM generation-switch driver repair

Status: implementation and focused local regression complete; real Linux native acceptance remains pending the next exact-head CI. No Docker, native package signing/build, commit, push or production write was performed here.

## Exact boundary

The observed gen3 native failure was Rocky case 23, serial 24: A mirrorlist and signed A metadata succeeded, then the second DNF invocation reread B's mirrorlist and four requests for B-prefix A-version RPM returned 404. DNF invocation order, bootstrap configuration, signature checking, exact downloaded hash/version and installed source/CLI validation remain unchanged. The planner peer supplies explicitly retained RPM routes; the driver now verifies their semantics instead of assuming all requests retain A's URL prefix.

Metadata responses must use exact A immutable paths and planned bytes. A switch still requires repomd and detached-signature requests. RPM responses must bind an existing plan object or explicit alias to a supplied stable original member, canonical architecture/family path, SHA-256 and size equal to the selected A payload. Equivalent original bytes may have a different verified audit source locator. Successful server write/flush is required; this is a server completion observation, not a claim of peer receipt. The native client exit, download hash and signatures independently remain mandatory.

Metadata 304 acceptance requires the same immutable URL, expected object ETag, empty response and matching request validator. If-None-Match takes precedence, and If-Modified-Since is only considered when it is absent. The fixture now follows that precedence; only existing public cache validator headers are recorded.

## Tests-first evidence

- Original native failure: pr74-ci-generation-3, exact head f2c37e92b09bcd11599d31449bbca5de0ac52c25; real CI executed 23/49, 22 passed, first Rocky switch failed. This is the actual failure evidence, not the new helper's missing-symbol result.
- All new driver regression cases were written before implementation and captured in tests-before-implementation.py. The final test file is byte-identical to this capture (SHA-256 c17f79a2b4e27d46890a18a3d92247cde146d065852a167752caceb65073eb8e).
- 01-driver-red.log records initial cases on old implementation; 02-complete-driver-red.log records the completed matrix, 4 methods with 43 missing-helper subcase errors plus the actual RouteTable failure (304 instead of expected 200 for conflicting validators).
- 03-driver-green.log: all 27 driver/fixture tests pass, including real bounded loopback TLS coverage already present.
- 04-related-green.log: all 61 driver and production-validator tests pass.
- git diff --check passes. Only the four owned files in this freeze were edited by this agent; the peer independently owns planner + planner tests.

Negative coverage includes undeclared aliases, unknown source/destination, wrong member/channel/architecture, wrong source/alias/delivered SHA or size, mutable alias cache classification, wrong object key, wrong requested version/family, absent or unknown metadata, B-prefix metadata, non-200 payload/metadata, incomplete writes, absent signature/mirror/payload, wrong first or later mirrorlist, and invalid 304 body/URL/validator/preference. Positive coverage includes cross-prefix planned alias, unchanged A original path, equivalent retained source locator, and same-byte existing retained original without an overlay alias.

## Limits

No claim is made that Rocky/Alma native switching or later cache/negative cases now pass. The fixed 49-case job is unchanged and the next exact-head native run must establish that result. Both baselines already passed real installed CLI in gen3; production public-origin acceptance and stable publication remain outstanding release gates.
