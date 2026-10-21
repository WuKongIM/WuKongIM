# Native preview fixture review

## Standards

# Standards — D3 M1 native preview fixture

0 documented-rule violations; 1 optional, low-priority heuristic finding.

**P3 — possible Mysterious Name (judgment call).** At [tests/native_object_delivery_fixture_drill.py:25–26](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-native-preview-fixture/tests/native_object_delivery_fixture_drill.py:25), `import native_stable_candidate_drill as N` and `import native_stable_clients_drill as K` hide which fixture owns subsequent calls. This driver crosses native construction, sealing, and command-recording boundaries; names such as `candidate_fixture` and `client_fixture` would make ownership visible at each call. Basis: [code-review/SKILL.md:45](/Users/tt/.agents/skills/code-review/SKILL.md:45), the supplied **Mysterious Name** heuristic, not a repository MUST. This is maintainability feedback, not a correctness or release blocker.

All 12 supplied smell heuristics were assessed; repository rules take precedence. The shared construction helper serves two current entrypoints, while the original client entrypoint retains its fixed-source and executable checks. No additional abstraction is warranted by this review.

The seven-file freeze and complete tracked/untracked patch match; applying the patch in memory reconstructs all reviewed bytes. All 12 producer and 10 control/public-key files match the base. Removing only the two new workflow steps restores the complete original workflow, including the native 31/client 49 invocations.

Applicable sources: caller root `AGENTS.md` (test-first, direct validation, resource bounds), its Workflow README, package README, release contract, object-delivery contract, CODEOWNERS, and `tests/contract.sh`. No applicable package AGENTS/FLOW was found. The original 29-case driver predates implementation; its sole later change adds command evidence. Workflow and `--init` contracts each have retained first-red evidence, satisfying the documented test-first requirement.

Reviewed receipts show the original PID-limit failure, then an uninstrumented 29/29 run under 1 CPU/512 MiB/128 PIDs with init, exit 0 and cleanup 0. Related/static checks passed; the earlier default suite records 566 passes and one existing RPM-tool skip. This is local evidence, not GitHub CI, installed-client, SDK, production provenance, or publication acceptance. No tests, source edits, CI, cloud calls, or production-key reads were performed by this reviewer.


### Alias finding closure

# M1 P3 alias closure

Closed the sole optional **Mysterious Name** finding at `tests/native_object_delivery_fixture_drill.py:25–26`. The module aliases now read `candidate_fixture` and `client_fixture`.

Independent static checks found exactly 27 changed identifier tokens, all belonging to these two renames. Reversing the aliases reproduces the complete original driver bytes; normalized ASTs are also identical. Every file matches the new seven-file freeze, and the other six files retain their originally reviewed digests. No test assertion or behavior changed.

New driver SHA256: `674bf8b06cedfb9811396dde1acf68a403947eae031207b26d1516cd4bf53884`.

The original report and binding are preserved. Standards now has 0 open findings. This closure performs no source edits, tests, CI, or repeat full review. Prior native evidence remains bound to its original source; the first PR CI run for the new head remains pending.


## Spec

# Independent Spec review — D3 M1

**0 findings:** no missing/partial M1 requirement, unauthorized scope, or incorrect implementation identified. Review base/head is `be32eb375e3c77dc5b3d1070fd798c281ae2d3bc`; all seven working files match the supplied freeze before and after review. Another 119 tracked files are byte-identical to base.

- Native export satisfies failure-matrix.md:5,10,16,21: `tests/native_stable_candidate_drill.py:321–326` removes TEST secret material before the original stable and preview verifiers; `:469–503` uses the existing verifier, canonical archiver and external audit receipt. `tests/native_stable_clients_drill.py:616–622` injects native preview only for the new caller; the original default remains structural. Production planner/materializer and twelve producer files are unchanged.
- Boundary requirements (failure-matrix.md:11–19,24) remain explicit: client export retains source/ELF guards at candidate helper `:204–222`; the new driver `:108–121,145–218` exercises their refusals, structural/tampered preview, exact input identities, opt-in and production-fresh rejection. Independent helper probes reproduced the three client refusals and TEST ONLY fresh refusal without native tools or network. Native receipts retain false provenance/activation fields; data-only packages do not claim installed CLI or production source truth (`:257–262`).
- Authorized CI wiring matches ci-scope-addendum.md:5–7. Removing only its two added steps reproduces the base workflow exactly, including old 31/49 commands. New execution at workflow `:198–240` is bounded, non-root, network-disabled, read-only, resource-limited and uses `--init`; only two public JSON receipts are uploaded.

Independently checked the saved local `ci-init-native-1` receipt: exact 29-case set, 54 command outcomes, no timeout/output-limit flags, expected rejection diagnostics, exit 0 and cleanup exit 0. Full output SHA was checked where the retained tail contains the complete output. The initial reviewer checker incorrectly required inventory order; its failed result and corrected set-based check are preserved, not classified as a source failure.

No native/full suite or CI was rerun. This is M1 local evidence, not GitHub CI acceptance, SDK M2, fresh production authentication, live CDN/public-client acceptance, or release readiness. Those exclusions follow failure-matrix.md:24 and the approved narrowed scope.


The final alias-only revision is independently bound by spec/alias-binding.json.

Standards: 0 open findings (one P3 closed). Spec: 0 findings. Exact-head GitHub CI is pending.
