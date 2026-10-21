# Standards — D3 M1 native preview fixture

0 documented-rule violations; 1 optional, low-priority heuristic finding.

**P3 — possible Mysterious Name (judgment call).** At [tests/native_object_delivery_fixture_drill.py:25–26](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-native-preview-fixture/tests/native_object_delivery_fixture_drill.py:25), `import native_stable_candidate_drill as N` and `import native_stable_clients_drill as K` hide which fixture owns subsequent calls. This driver crosses native construction, sealing, and command-recording boundaries; names such as `candidate_fixture` and `client_fixture` would make ownership visible at each call. Basis: [code-review/SKILL.md:45](/Users/tt/.agents/skills/code-review/SKILL.md:45), the supplied **Mysterious Name** heuristic, not a repository MUST. This is maintainability feedback, not a correctness or release blocker.

All 12 supplied smell heuristics were assessed; repository rules take precedence. The shared construction helper serves two current entrypoints, while the original client entrypoint retains its fixed-source and executable checks. No additional abstraction is warranted by this review.

The seven-file freeze and complete tracked/untracked patch match; applying the patch in memory reconstructs all reviewed bytes. All 12 producer and 10 control/public-key files match the base. Removing only the two new workflow steps restores the complete original workflow, including the native 31/client 49 invocations.

Applicable sources: caller root `AGENTS.md` (test-first, direct validation, resource bounds), its Workflow README, package README, release contract, object-delivery contract, CODEOWNERS, and `tests/contract.sh`. No applicable package AGENTS/FLOW was found. The original 29-case driver predates implementation; its sole later change adds command evidence. Workflow and `--init` contracts each have retained first-red evidence, satisfying the documented test-first requirement.

Reviewed receipts show the original PID-limit failure, then an uninstrumented 29/29 run under 1 CPU/512 MiB/128 PIDs with init, exit 0 and cleanup 0. Related/static checks passed; the earlier default suite records 566 passes and one existing RPM-tool skip. This is local evidence, not GitHub CI, installed-client, SDK, production provenance, or publication acceptance. No tests, source edits, CI, cloud calls, or production-key reads were performed by this reviewer.
