# D2 Standards review

Scope: uncommitted eight-file change at `d824fe37918ee4ef22b1b1745bc02518812563e3`, including all six untracked files. Supplied freeze and patch hashes match; every patch output reconstructs the current bytes. All other 116 tracked files are unchanged. The Spec report was not read.

## Hard requirement — one known process violation

**[P2] Two HTTP wiring unit tests were added after implementation.** `tests/test_object_storage_delivery_inputs.py:199–214` contains the two `FreshPreviewWire` tests that, according to the frozen author `README.md:21` and `fresh-boundary-first-run.log`, were written after the corresponding wiring and first ran green. This violates the supplied root `AGENTS.md:129` (“NEVER write unit tests after you write code”), also reflected by the explicitly frozen all-failures-first order in `initial-binding.json`. Later dirty-executor and distinct-ConfigId red/green evidence does not repair that historical ordering. Keep the useful tests and explicit disclosure; do not manufacture a retrospective red or describe this whole change as tests-first. This is a process finding, not evidence that the implementation is incorrect.

## Judgment-based smells

No additional actionable finding from the supplied twelve-smell baseline. The instance transport seam addresses current SDK isolation needs; dictionaries preserve existing JSON/provider contracts. Adding abstraction solely to eliminate these shapes would not improve this narrow change. No further documented-standard breach found.

## Boundaries checked

The real SDK constructs/signs requests before the injected HTTP adapter returns fixtures (`tests/provider_sdk/test_aliyun_object_storage_provider.py:136–145`); socket connections are prohibited. CI is contents-read only, uses pinned Actions and hash-locked wheels, and rejects zero/skipped SDK tests. Dependency installation itself is not network-free. The adapter does not supply an activation path, consistent with `docs/object-storage-delivery.md:155–170,188–216`; production controls/client validators are unchanged.

Author evidence records SDK **17**, input **10**, and default **567** tests with **one existing Mac skip**. These were inspected, not independently rerun. Linux CI, cloud wire equivalence, and a complete production-shaped v2/native-signature positive path remain unverified. No provider, credentials, keys, GitHub, source edits, or full-suite execution occurred in this review.
