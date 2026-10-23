# Standards review: candidate 5f

Scope: `git diff d08031d576c3497c56b0dfba60e4db8370224c04 5f679978307aa1a5d57ec18a4ed548de79be0da8`; merge-base is the supplied d080. Head tree: `061aa8b1f18c4cae4c16454b602f799fc9ac7b44`. Reviewed all 21 changed files and five commits: 83c0be93, 8100d7fd, 7f36a7cc, 4bab5500, 5f679978. This is the Standards axis only; another reviewer owns Spec.

## Findings

No blocking documented-standard violations or actionable baseline code smells identified.

- Layering and cluster-authoritative permission reads are preserved. The five-millisecond collection change retains existing ownership/execution limits and fresh-read fences. No business shortcut, new global aggregate service, configuration surface, or dependency inversion was added.
- ACK publication uses one narrowly owned atomic gauge with an English constraint comment. Sampling/clone handling preserves zero and historical samples. Actor-ready observations publish their existing lack of a fixed item bound; admission and queue depth remain unchanged. Neither change weakens parser correctness guards.
- Real elapsed-time/hold and process tests added by this diff use integration tags; the new fast gauge/actor checks do not add realistic sleeps. ACK progress tests exercise actual runtime public calls with joined cleanup. Existing normal/race and E2E feedback was supplied; this review ran no Go tests or product.
- Unreleased CHANGELOG entries, PROJECT_KNOWLEDGE, affected module FLOW files, and generated FLOW index document the changed behavior. Read-only verification of the canonical named `flow-doc-contracts` feedback found exit 0, 88 compliant files, 0 invalid, 20 existing warnings. Receipt SHA: `a128b9f2b66ee64de6e9884a3fdded96649285594a54cfaf10e19ce914703db0`.
- Reviewed the Fowler smell baseline; the small helpers and typed queue-family distinction address current behavior without speculative abstractions. No requested refactor arose from this heuristic review.

## Independence and limits

I authored the storage-capture script/tests; root independently reviewed that producer diff and unchanged contracts. This part is not represented as an independent author review. Its pre-code frozen tests, real RED, normal/race GREEN receipts remain in `permission2000-storage-capture-diagnostics-v1`. Historical CI missing-evidence cause remains unknown. Source review and component feedback do not establish repaired message loss, full Linux acceptance, or readiness to release.

## Frozen instructions

- `AGENTS.md`: `2220f49835b8dba20bafbc277854421222765be52e874fc6ee994bcd3ee0d746`
- `internal/app/FLOW.md`: `97552ae0d95e4957c4005b6c6af0a961414eb9f9dec93faf7e15d5a89b28405b`
- `internal/bench/FLOW.md`: `e43555b18ed6de239f2a7521f3ef63c63e438d5f9fa9d7638a4d8f0a3dfe4e73`
- `pkg/gateway/FLOW.md`: `f416ef00d5f739c06eabff2577feb409361c4756ba1d848f5c0158ec8d4a8c6a`
- `pkg/slot/FLOW.md`: `4b1f17c5f088e5d3454ba707eec9fc4f70ec830062a0bc830f117f298679ac07`

Also read CONTEXT, domain/issue-tracker conventions, and relevant observability/saturation ADRs 0022/0024/0046. No more-specific AGENTS or applicable FLOW exists in scripts, metrics, or target beyond the listed ancestors. Tooling-enforced formatting is not reported as a smell.
