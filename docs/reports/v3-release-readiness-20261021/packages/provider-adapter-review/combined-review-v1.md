# D2 provider/input library first review

## Standards

# D2 Standards review

Scope: uncommitted eight-file change at `d824fe37918ee4ef22b1b1745bc02518812563e3`, including all six untracked files. Supplied freeze and patch hashes match; every patch output reconstructs the current bytes. All other 116 tracked files are unchanged. The Spec report was not read.

## Hard requirement — one known process violation

**[P2] Two HTTP wiring unit tests were added after implementation.** `tests/test_object_storage_delivery_inputs.py:199–214` contains the two `FreshPreviewWire` tests that, according to the frozen author `README.md:21` and `fresh-boundary-first-run.log`, were written after the corresponding wiring and first ran green. This violates the supplied root `AGENTS.md:129` (“NEVER write unit tests after you write code”), also reflected by the explicitly frozen all-failures-first order in `initial-binding.json`. Later dirty-executor and distinct-ConfigId red/green evidence does not repair that historical ordering. Keep the useful tests and explicit disclosure; do not manufacture a retrospective red or describe this whole change as tests-first. This is a process finding, not evidence that the implementation is incorrect.

## Judgment-based smells

No additional actionable finding from the supplied twelve-smell baseline. The instance transport seam addresses current SDK isolation needs; dictionaries preserve existing JSON/provider contracts. Adding abstraction solely to eliminate these shapes would not improve this narrow change. No further documented-standard breach found.

## Boundaries checked

The real SDK constructs/signs requests before the injected HTTP adapter returns fixtures (`tests/provider_sdk/test_aliyun_object_storage_provider.py:136–145`); socket connections are prohibited. CI is contents-read only, uses pinned Actions and hash-locked wheels, and rejects zero/skipped SDK tests. Dependency installation itself is not network-free. The adapter does not supply an activation path, consistent with `docs/object-storage-delivery.md:155–170,188–216`; production controls/client validators are unchanged.

Author evidence records SDK **17**, input **10**, and default **567** tests with **one existing Mac skip**. These were inspected, not independently rerun. Linux CI, cloud wire equivalence, and a complete production-shaped v2/native-signature positive path remain unverified. No provider, credentials, keys, GitHub, source edits, or full-suite execution occurred in this review.

## Spec

# Spec review — 2 findings (both P2)

Scope: eight frozen working-copy files at `d824fe37918ee4ef22b1b1745bc02518812563e3`; no branch commits. All eight hashes match the supplied freeze; all other 116 tracked files are byte-identical. Standards was not consulted.

1. **[P2] Bind the checked configuration body to its digest before writing.** `scripts/aliyun-object-storage-provider.py:282–298` validates caller-supplied `before.configs`, but the fresh observation fence compares only `before.sha256`. Deleting an unmanaged rule from that body while retaining its original, still-current digest permits one real SDK `BatchSetCdnDomainConfig` call; only the subsequent readback returns `incomplete`. The unmodified observation rejects with zero writes. Spec: `provider-adapter-next-slice/README.md:40`, “未知规则/需要删除旧项先拒绝”; `wire-followup/README.md:23`, “按整份真实相关配置检查priority及管理边界”. Recompute/validate the observation digest, or validate the fresh observation itself, before any write. `probe.json` records both cases.

2. **[P2] Reject debug logging before entering the signing SDK.** The guard at `scripts/aliyun-object-storage-provider.py:95–96` runs inside transport, after `_call` at line 167 invokes SDK signing/logging. With `aliyunsdkcore.client` at DEBUG, official Core 2.16.1 logs `SecurityToken` before this guard rejects. Our probe used only a fake token, made zero adapter/network calls, and captured the token in the SDK log. Spec: `failure-matrix.md:12` requires sensitive-output rejection; the operator contract (`docs/object-storage-delivery.md:176`) says SDK debug logging is rejected. Move the guard before SDK invocation/signing; retain transport checks as defense in depth.

The independent offline probe intentionally exits 1; its script, JSON, stderr and sanitized TEST ONLY log are retained. No source changes, real credentials, provider requests or full-suite rerun occurred. SDK/library scope and unconditional production activation refusal otherwise remain intact. The disclosed lack of a complete production-shaped v2/native-signature positive integration path remains a coverage limitation; 17 SDK, 10 input and 567 default tests do not establish that acceptance or live publication.

Standards: 1 P2 process finding. Spec: 2 P2 implementation findings. Historical tests-after-code order is retained; new repairs require tests first. No production authority or publication is established.
