# Differential Spec review: v2

Disposition: `SPEC-P2-01` closed for freeze-v2. Original v1 finding and its evidence remain unchanged under `/tmp/wukongim-v3-readiness-20261021/cloud/downward-budget-review/spec`.

Spec: [design line29](/tmp/wukongim-v3-readiness-20261021/cloud/post-systemd-repair-execution-options-generation-2/README.md:29) requires “失败不能阻止已有Lease Release/zero”; failure matrix C11 requires zero publication after consumer rejection.

- [rehearsal upload518](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-budget-downward/.github/workflows/chat-lifecycle-rehearsal-finalize.yml:518) and [formal upload456](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-budget-downward/.github/workflows/chat-lifecycle-formal-finalize.yml:456) now use `always() && steps.cleanup.outputs.zero == 'true'`. This removes implicit success gating while retaining the existing authenticated zero prerequisite. No zero=false/pending/unobserved path can pass this condition.
- Independently compared each entire workflow against saved v1: exactly one string replacement each, no other byte changed. Upload action/version/name/path/retention and all cleanup/resource/qualification steps are identical. Rehearsal450 and formal431 still exit1 at their respective budget rejection boundaries.
- [new contract](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-budget-downward/scripts/chat_lifecycle_downward_budget_consumers_test.go:58) protects both conditions and verifies an earlier non-continue-on-error budget gate still ends `|| exit 1`. Actual consumer test file is byte-identical to v1; retained13 run passes current/previous-final, stop/hard boundaries, validation failure and local cleanup checks.
- According to [GitHub status semantics](https://docs.github.com/en/actions/reference/workflows-and-actions/expressions#status-check-functions), always() overrides default success(). `truth-table.json` records all combinations. This is a source/contract audit, not a dispatched workflow or actual artifact-upload proof.

## Freeze

Base `ff0aa096fe1bd125be70fc1844b2f45cc7f0115e`; all27 working files match v2. Full patch SHA `9c6fa1bb330b10f79359af1905e74b47b04adff34de8e1b7b865e563ef1ceef3`. Exactly three files differ from v1:

- `.github/workflows/chat-lifecycle-formal-finalize.yml` SHA256 `43af3a24f78aa9bca4354c6c192de5a6f037b3686be0fb13f20e2b7c529cef05`.
- `.github/workflows/chat-lifecycle-rehearsal-finalize.yml` SHA256 `30b29a1b652682d749ca11599806fb6b370953636031bad7872b9640e5a0ec6f`.
- `scripts/chat_lifecycle_downward_budget_consumers_test.go` SHA256 `c3aa906f6a8998cb2eda71915a0c8ba3f9abe4e2b24f35a2d2b2dcedb4d8b459`.

## Tests and limits

12-zero-upload-gating-red records failure of both rehearsal/formal subtests, their root and package.13-zero-upload-and-consumer-green contains no failed tests/packages; includes both new gating arms and existing real consumer-shell rejection/cleanup tests.10 is preserved as a test-draft step-name error;11 is preserved as failure before the production change applied, never treated as green. No suite repeated during this read-only review. Seven named checks are outside this review and remain author-owned. The repair does not establish a new product candidate or paid/cloud qualification.
