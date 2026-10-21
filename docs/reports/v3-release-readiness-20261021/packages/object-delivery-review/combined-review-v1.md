# Original D offline delivery review — v1

Fixed base 983e567f155a982c45ca066e4dc67d33851f0d24, five-file frozen patch 0d1cf456a61d59072264efaf7b1a4b282089ec6807d9c58b8b0f702c5e9b6f0b.

## Standards

# Standards review

No actionable documented-standard violations or baseline smell findings in the five-file frozen working-copy change.

The review covers `git diff HEAD` plus every untracked file, with no branch commits, at `983e567f155a982c45ca066e4dc67d33851f0d24`. All five file hashes/sizes and the reconstructed complete patch match `implementation-freeze.json`. No package-specific AGENTS/FLOW or coding-standard file was found, including ignored paths. The enclosing AGENTS bytes match both `rules-freeze.json` and its recorded source revision.

- The new Unreleased entry (`CHANGELOG.md:5`) satisfies AGENTS “User-visible changes MUST add a concise user/operator-facing entry.”
- `prepare_delivery` and `simulate_delivery` explain their responsibilities and constraints (`scripts/prepare-object-storage-delivery.py:186`, `:316`). The seam is used by the current failure tests, with bounded archives/routes/evidence; I found no actionable over-engineering or smell under the mandatory code-quality rule and the twelve supplied heuristics. Smell assessments are judgment calls, not hard standards.
- The failure inventory and all 20 original tests precede implementation in the recorded first-red evidence. Comparing the preserved first-red test bytes with the reviewed tests shows only the two documented fixture corrections. This supports AGENTS “FIRST write all the ways it could fail” and does not indicate tests written after code. The documented user-authorized seam testing is a reasoned deviation from the E2E preference.
- The README and operator guide preserve the existing offline, disabled-stable and public-verification boundaries. No live publication or production authorization is claimed.

This was a static, read-only review. Recorded green evidence was inspected; tests were not rerun. No implementation, cloud, CI, key, or network state was changed.

Standards: 0 findings; no worst issue.

## Spec

# Spec review — original D offline slice

Fixed HEAD/base: `983e567f155a982c45ca066e4dc67d33851f0d24`; working-copy scope: `git diff HEAD` plus the three untracked files in `implementation-freeze.json`. All five file hashes, the complete patch hash, frozen supporting sources, and three frozen design inputs match. Static review only; no tests, CI, cloud calls, source edits, or private material access.

1. **[P2] Current activation control cannot be fenced by the fake seam.** `scripts/prepare-object-storage-delivery.py:370–375` rechecks transport inputs and historical source/candidate controls, then preflight/configuration. However, `_preflight` accepts only resource/versioning/fresh fields (289–295); the independent `activation_control_sha` is merely copied (195–196, 253, 333). A changed current activation control with unchanged historical inputs/configuration has no observable representation and cannot stop simulated activation. Approved `after-native-delivery-gap-review/failure-inventory.md:19` requires “激活前source/audit/control/current-config漂移 | 不执行新的激活动作”; README:11 requires “保留候选历史控制、seal 控制及当前 activation 控制的独立字段/摘要”. Bind a fake current-control observation to the requested activation SHA and compare it before apply, while leaving real GitHub resolution deferred.

2. **[P2] Stable verifier receipts are not matched to their transport input/plan.** `_client_result` (`scripts/prepare-object-storage-delivery.py:298–313`) checks target identity but ignores the original stable verifier's `transport_inputs_sha256` and `transport_plan_sha256` (existing verifier:1591–1592). A receipt from a different composite with the same stable candidate but different preview/retained generations is accepted as `simulated_complete`; the new fixture omits both fields (`tests/test_object_storage_delivery.py:109–125`). Approved README:23 requires “绑定输入/plan、source/audit/control、逐对象确认、目标资源、before/after 配置摘要、公开入口检查、原客户端 receipt 摘要和 exact target”. Compare both original receipt digests with this preparation and cover mismatched/missing digests using the fake verifier; retain the returned receipt unchanged.

No scope creep found. v1/v2 normalization, source-based alias materialization, create-only GET confirmation, exact route precedence, disabled production flags, and observed partial configuration effects match the approved scope. Real SDK/API adaptation remains deliberately outside this slice.

Standards: 0 findings; Spec: 2 findings, worst P2.
