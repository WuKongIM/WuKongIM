# D2 provider/input library v2 independent review

## Standards

# D2 v2 Standards review

**No new findings. One historical process P2 remains.** Review covers eight uncommitted files at `d824fe37918ee4ef22b1b1745bc02518812563e3`. All eight hashes, supplied patch and its reconstructed outputs match; the other 116 tracked files are unchanged before and after review. No package-local AGENTS/FLOW was found. The Spec report was not read.

**Hard requirements.** The existing finding concerns two HTTP wiring tests at `tests/test_object_storage_delivery_inputs.py:199–214`, written after their implementation. Supplied root `AGENTS.md:129` says “NEVER write unit tests after you write code.” This v2 does not erase that historical violation; retain its original report and disclosure. No retrospective red is credited.

The five new SDK test methods (`tests/provider_sdk/test_aliyun_object_storage_provider.py:269–346`) have genuine retained first-red evidence: 22 methods, 24 failing subcases against the unchanged v1 provider. The red test bytes equal the final test bytes. Existing green receipts show 22 SDK tests without skips, 10 input tests, and 567 default tests run with one existing skip. These were inspected, not rerun; do not describe the latter as 567 passing plus a skip.

New guard responsibilities are documented in English (`scripts/aliyun-object-storage-provider.py:55–63,266–276`), satisfying `AGENTS.md:88–89`. `CHANGELOG.md:5–8` supplies the operator-facing Unreleased entry required by `AGENTS.md:101–102`. The narrow shared logging guard and complete-observation binder address present defects; no additional actionable smell was found. The twelve supplied smells remain heuristics, subordinate to `AGENTS.md:86–87`; introducing more types or forwarding layers is unnecessary here.

The unchanged CI runs mandatory SDK contracts with pinned Actions, hash-locked dependencies and contents-read permission (`.github/workflows/object-storage-provider-contract.yml:18–44`); its runner rejects zero/skipped tests. Dependency installation uses network; provider test sockets are forbidden. No publishing path was introduced: `activate()` still rejects, and documentation preserves unverified live routing/concurrent logging boundaries (`docs/object-storage-delivery.md:155–181,192–222`).

No source changes, tests, provider calls, credentials, private keys or external writes occurred. Exact-head Linux CI and live provider/public-client acceptance remain unverified.

## Spec

# Spec v2 review — original findings closed; no new findings

Scope: complete eight-file working-copy change at `d824fe37918ee4ef22b1b1745bc02518812563e3`, with the four-file repair delta inspected against v1. All eight hashes match the v2 freeze; the other 116 tracked files remain identical. No Standards report was read.

- **Original configuration-body P2: CLOSED.** Spec requires “未知规则/需要删除旧项先拒绝” (`provider-adapter-next-slice/README.md:40`) and management checks against the complete real observation (`wire-followup/README.md:23`). `aliyun-object-storage-provider.py:266–276` now freezes the exact observation shape and binds its full body, digest, domain and resource. Lines 310 and 327–328 apply that binding before interpretation and compare the complete fresh observation before writing. Replaying the former hidden-rule probe now rejects with zero writes. Rehashed hidden rules, unknown rows, wrong domain/resource, incomplete/extra fields also reject; a legitimate managed update still uses exact ConfigId 90 and verifies readback.

- **Original late logging-guard P2: CLOSED.** The sensitive-output requirement (`provider-adapter-next-slice/failure-matrix.md:12`) is now checked before SDK construction (line 147), RPC signing (174), and every OSS entry (191, 196, 209, 243); transport retains its check. The original DEBUG/token probe and inherited parent/root/environment variants reject before adapter calls, without logging fake credentials or changing global logger/environment state. Documentation lines 176–180 accurately retain the limitation: unrelated concurrent global logger reconfiguration is not isolated.

Independent locked-SDK offline probe: **9 configuration cases and 28 logging cases pass**, exit 0, no provider sockets or real credentials. Probe source/results are retained. Original v1 findings/evidence remain unchanged. No source edits or full-suite rerun occurred.

Production activation remains unsupported; these results do not establish complete production-shaped v2/native-signature integration, live PCRE/query equivalence, public-client acceptance or a release. Those disclosed verification limits remain open outside this repair's two findings.

Standards: 0 new findings; 1 historical process P2 retained. Spec: both original P2 findings closed, 0 new findings. These parallel independent reviews do not certify pending Linux CI, production-shaped v2/native success or live publication.
