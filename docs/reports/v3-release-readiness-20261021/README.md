# V3 readiness evidence — 2026-10-21

Read [the current assessment](../2026-10-21-v3-release-readiness.md) for the
release decision. This directory preserves bounded audit results and local
regression attempts; it is not a published release or a long-run qualification.

- `context.json`, `recovery-rules.json`: exact source identities and frozen
  AGENTS/FLOW digests for the evidence and recovery worktrees.
- `ci/summary.json`, `ci/readiness-matrix.md`: initial read-only GitHub audit.
  The empty PR list is the state at that audit time, before delivery PR creation.
- `packages/summary.json`, `packages/readiness.md`: live public status, immutable
  audit identity and protected-state observations; not a fresh public-client run.
- `packages/implementation-validation.json`: PR #67 source-classification scope,
  test-first evidence, 38 focused tests and static contract results. Production
  stable remains disabled. Initial CI states are timestamped observations.
- `local/coverage-summary.json`: package-by-package default-test coverage across
  infrastructure recovery attempts. 219 pass, 15 no-test packages; no product
  assertion failure. Both disk-exhaustion and missing-cache runs remain retained.
- `spec/self-chat/`: failed fixture attempts, corrections, final source and JSON.
  Final result covers 12 ACKs, 12 complete-history observations and two restarts.
  Repeated real-time frames are distinct from duplicate durable messages.
- `recovery-checks/`: named local checks, exact commands, source/diff identity and
  logs. CI on the final PR source is recorded independently.
- `cloud/`: bounded read-only preparation audit, exact public configuration,
  frozen control sources, historical cleanup payloads and their limitations.
  Those original preparation receipts did not establish provider inventory or
  authorize procurement. The subsequently authorized exact cloud request is
  recorded separately below.
  `local-shakeout-preconditions.json` records the initial host below the existing
  5-percent disk reserve; no local 30-minute run had then been attempted.
  `disk-followup/` measures the same reserve after merge and inventories only
  previously recorded task output paths. Even all those outputs are too small
  to recover the missing capacity; no files were deleted or workload attempted
  during that audit. Later free-space recovery and the first workload are
  separately recorded below.
- `ci/session-cleanup-probes/`, `ci/candidate-workflow-validation.json`,
  `review.md`: original cross-PGID cleanup failure, real-process macOS probes,
  static validation and independent review. Linux results are separate CI evidence.
- `audit-archive-manifest.json`: original and archived byte hashes for the cloud
  and cleanup audit; `.log` files are retained as deterministic gzip streams.
- `mqtt-diagnosis/`, `replication-diagnosis/`, `diagnosis-archive-manifest.json`:
  first-candidate CI failures, deterministic red/green investigations and focused
  regression receipts. MQTT changes product cancellation; replication changes
  only test fault injection, retaining the original 66-item obligation.
- `candidate-v2-rules.json`, `candidate-v2-dispatch.json`, `followup-checks/`:
  updated candidate identity, exact rule digests, first qualification dispatches
  and the named FLOW check (including the initial stale-index failure).
- `ci/final/first-generation-summary.md`: all six first-generation CI outcomes,
  actual executed/skipped steps, source-tree identity and workload numbers.
  Its allowlist retains bounded reports and logs; archive-level digest claims
  distinguish full downloads from CRC-checked selected ZIP members.

- `ci/final-v2/second-generation-summary.md`: seven updated-candidate CI runs, all
  first-attempt successes, exact source/binary identity, full matrix and native
  lifecycle results; original first-generation failures remain preserved.
- `packages/preflight-followup/`, `packages/preflight-ci/`: test-first source
  preflight implementation, independent review, and both new CI results including
  seven actual native metadata cases. No production deployment occurred.
- `packages/public-signing-observation.json`, `local-backup-classification.json`
  (under `packages/`): public fingerprint comparisons only. The latest user
  clarification at that historical audit confirmed independent stable keys did
  not yet exist. The subsequently generated identities are recorded below.
- `packages/storage-design/`: source-frozen OSS/CDN design and official sources;
  not an implemented transport or cloud authorization.
- `review-merge/`: product PR #1016 merge evidence. The disabled Review Agent
  observation remains preserved; the resolution records the user-authorized
  normal administrator merge, matching source trees and verified cleanup.
- `packages/merge/`: PR #67 rebase merge, unchanged branch protection, matching
  source trees, and successful main validation with Pages deployment skipped.
  The original clean checkout is retained because its pre-rebase tip is not an
  ancestor of main; no unmerged local branch was force-deleted.
- `packages/stable-signing-user-confirmation.json`: evolving user statements.
  NAS availability supplies a potential encrypted-backup location, not an
  established offline/hardware signing environment or generated stable keys.
- `packages/stable-environment-options/`: exact contract and official platform
  evidence. A second computer is not required; the existing Mac may support an
  isolated external system, subject to an implemented and verified ceremony.
- `packages/stable-local-custody-decision.json`: the user's subsequent explicit
  choice of Mac/NAS custody supersedes the offline/hardware-only requirement
  for this work. The choice itself did not create keys or enable a release;
  subsequent key generation and #69 implementation are recorded below.
- `packages/stable-signing-protocol-plan/`: proposed Mac-local public-key and
  request preparation, then two-stage native signing/receipt verification.
  This protocol proposal is historical and is no longer the implementation
  plan: the existing family signer already performs the needed local sequence.
- `packages/stable-local-signer-route/`: source-backed scope reduction to a
  small local-file CLI adapter, reused native TEST ONLY signing, and Mac key
  preparation. No new exchange protocol or session ledger is required.
- `packages/object-storage-plan/`, `packages/transport-plan-review/`:
  failure-first implementation, independent resource-limit finding and fix,
  then ordinary-name RPM metadata support with complete closure validation.
  The two generations and their rejection evidence remain distinct.
- `packages/transport-plan-real/`: complete public beta.24 archive digest and
  historical identity anchors; first unsupported-metadata rejection and the
  subsequent successful 44-file plan. The large tar is excluded. This is byte
  and structural consistency evidence, not cryptographic or client acceptance.
- `packages/transport-plan-delivery.json`: historical draft PR #68 identity;
  `packages/transport-plan-archive.json` records the bounded allowlist copies.
- `packages/transport-plan-ci/`: both first-attempt #68 PR checks, 373 Python
  tests and the TEST ONLY native drill; actual merge/head trees match.
- `packages/transport-plan-merge/`: separately user-approved #68 normal rebase
  merge to `45619df14`, identical approved tree, unchanged protection, and
  373 passing postmerge tests. All Pages publication steps were skipped.
- `cloud/bundle-ed705/`: first credential-free offline bundle build and full
  artifact verification against exact ed705 source/control; no cloud resources
  or deployment. The archive excludes the large raw artifact.
- `cloud/local-shakeout-ed705/`: frozen first 30-minute precheck attempt after
  disk recovery. It failed early with grant `offered_load_under_delivery` and
  produced no final report. Running cuts do not establish full correctness;
  all 12 owned processes and 64 reserved ports were independently cleaned.
- `cloud/local-shakeout-grant-diagnosis/`: real-factory fixed-clock diagnosis,
  default-budget red/green regression, and unchanged-threshold repair. Five
  named checks passed; both full macOS scripts-integration attempts hit the
  original nine-minute package limit. Subsequent Linux failures and the fresh
  local workload are recorded separately below. The nested `runner-repair/` contains failure-preservation and
  owned-session cleanup probes for the external local runner, not product load.
- `cloud/local-shakeout-repair-review/`: independent mechanism and implementation
  review, including the pre-existing test-only lock-copy vet correction.
- `postmerge-preparation-archive.json`,
  `planner-merge-and-shakeout-archive.json`: verified allowlist copies and
  original-file hashes for these new bounded receipts.

- `cloud/local-shakeout-0672c1159-500/`: completed local helper with 1,753.104
  measured seconds, 876,529 SEND/ACK, zero correctness or under-delivery errors
  and verified process/port cleanup. Two retries, 102 hot requests over 400 ms,
  presence warnings and shutdown deadline warnings remain explicit; this is
  a local precheck, not the planned cloud soak.
- `ci/shakeout-1017/`: first repaired-budget candidate CI, including the full
  Linux scripts timeout; other passing jobs cannot replace that failed gate.
- `ci/scripts-timeout-readonly/`, `ci/scripts-json-observation-39565861218/`:
  first timeout analysis and subsequent bounded JSON observations. They retain
  real assertion failures, cold-build cost and the diagnostic flag's own text
  format interference without assigning every failure to one cause.
- `ci/scripts-vcs-build-repair/`, `ci/scripts-profile-fixture-portability/`:
  cached compilation with a verified fresh link and text-format inspection,
  plus literal grep assertions removing an undeclared rg dependency.
- `ci/scripts-signal-analysis-diagnosis/`, `ci/scripts-shell-cleanup-review/`:
  Bash 3/5 differential probes, failing-before/fixed-after cleanup tests and
  independent review. The original observer exit 255 remains unclassified;
  the next complete Linux gate finished in 533.570 seconds with 600 passes,
  6 skips and one different signal-join test returning 255. The old observer
  passing that run does not establish a shared cause or complete repair.
- `packages/stable-local-request/`, `packages/pr69-native-failure/`,
  `packages/pr69-ci-c217/`, `packages/pr69-merge/`: #69 local signer adapter,
  preserved original fixture/allowlist failures, passing final native drill
  and authorized merge to `8b3ae9a08`. Postmerge validation passed; publication
  steps skipped and stable remained disabled.
- `packages/mac-key-preparation/`: reviewed one-time preparation program,
  TEST ONLY rehearsal and public production receipts/certificates. New distinct
  APT/RPM identities were generated on the Mac, encrypted backup restoration
  and public-only signature checks passed. Private material and Keychain
  passwords are excluded; NAS backup has not been performed.
- `packages/stable-public-registration/`, `packages/pr70-ci-0c26496b/`:
  #70 public-only key registration and validation, independent review,
  400+3 tests and the full signed-repository drill. Both first CI attempts
  passed. The separately approved normal rebase merge is recorded in
  `packages/pr70-merge/`: source trees match, postmerge 400+3 tests passed,
  publication steps skipped. The channel remains disabled.
- `local-completion-archive.json`, `signing-and-scripts-archive.json`: bounded
  allowlist copies with original sizes/hashes for this continuation.

- `packages/stable-bootstrap-preparation/`, `packages/stable-bootstrap-preparation-review/`,
  `packages/pr71-ci-fb62e03/`, `packages/pr71-merge/`: independently reviewed #71,
  410+4 checks, six native coexistence/reproducibility cases and the existing signed
  repository drill; authorized merge to `0cd625c`, exact-tree postmerge checks
  passed and deployment skipped. Preparation packages remain unsigned/unpublished.
- `ci/scripts-combined-d7affe-final/`: all previous candidate workflows terminal;
  E2E/SDK/native/chat passed, with the original scripts failure still retained.
- `ci/scripts-wait255-observed-failure/`: bounded Linux trace proves the current
  shakeout join succeeded before the fixture's auxiliary signaler wait returned
  internal -1/external 255. This was the diagnosis-stage state; final repair and
  complete first-attempt Linux acceptance are recorded separately below.
  `ci/scripts-wait255-upstream-research/` retains supporting upstream clues, not
  an independently confirmed identity for the Ubuntu shell bug.
- `public-release-recheck/`: fresh public release list still headed by beta.24;
  the GitHub latest endpoint excludes prereleases and is not the v3 version list.
- `bootstrap-and-wait-diagnosis-archive.json`: bounded copied-file identities for
  this continuation, preserving original receipts rather than overwriting failures.

- `ci/scripts-wait255-final/`: remaining terminal outcomes from the trace-only
  generation; these do not erase its failed scripts gate.
- `ci/scripts-wait255-fixture-repair/`, `ci/pr1017-final-composition-review/`:
  test-first exact-process Go signal fixture, original-status and cleanup
  controls, Bash 3/5 validation and complete final diff review. One empty failed
  GitHub read stdout remains explicitly listed in `archive-validation.json`.
- `ci/scripts-wait255-linux-final/`, `ci/pr1017-ready/`: ab75c226's four first
  successful workflows, full downloaded candidate artifacts, 602 scripts passes
  and six skips, five migration topologies, cross-node/self-chat regressions;
  the preserved ready/open state before separately approved merge. Final reports distinguish
  recomputed artifact hashes from a declared binary digest without binary bytes.
- `packages/stable-local-repository-plan/`,
  `packages/stable-offline-candidate-native-contract-review/`,
  `packages/stable-offline-candidate-identity-review/`: bounded unpublished
  candidate design, corrected existing RPM signer allowlist contract, and
  original accepted preview/symlink findings. These are planning/partial-review
  evidence, not final native Linux or public-client qualification.
- `packages/stable-offline-candidate-local-reviewed/`,
  `packages/stable-offline-candidate-core-review/`: frozen 16-file local
  implementation, original red/green records, complete core and native-slice
  reviews. Local 446 tests (one environment skip) and four contracts passed;
  this generation explicitly does not claim that the first native CI passed.
- `ci/pr1017-merge/`: explicit user authorization, exact-head ordinary merge
  fc182212, matching reviewed tree, preserved unrelated local main, clean task
  worktree/branch removal and successful automatic documentation publication/CDN
  refresh. A failed read-only guard/download is retained separately from CI.
- `cloud/rc-plan-fc182/`: exact merged-source offline RC plan and input templates,
  frozen source digests, aggregate budget and authorization/runtime boundaries;
  no Quote/Acquire/dispatch was performed. Premerge draft history remains intact.
- `packages/pr72-ci-generation-1/`: first candidate-key fixture failure before
  any native candidate case ran; preceding metadata/signing/bootstrap passes
  and later skipped repository drill are explicit. Pages446+4 passed. Mac
  long/short GPG path probes support a concrete path defect without asserting
  the original Linux exception had independently proved the same cause.
- `signal-repair-and-stable-plan-archive.json`: original source paths, sizes and
  hashes for this continuation's public allowlist copies.

Logs and JSONL may be gzip-compressed to retain exact bytes without bloating the
working copy. Decompress with `gzip -dc FILE.gz`; no rejected attempt is removed.
`SHA256SUMS` covers every archived file except itself. Paths and times inside the
receipts describe the original execution environment; replay commands need fresh
output directories and the recorded product revision/binary identity.

The source evidence branch remains separate from the small product repair PR to
respect the repository's review-size limit. This does not change or hide the
candidate's original failures. A final tag still requires its own release gates,
immutable publication and signed native-package public-client acceptance.

- `cloud/rc-readiness-fc182-current/`, `cloud/rc-plan-fc182-root-review.json`: fresh
  read-only GitHub inventory and independent exact-source plan verification.
- `cloud/rc-start-fc182/`: explicit human authorization, four-input request, public
  diagnostic key and source/control guard; private keys are excluded. Its
  `identity-boundary-review/` independently distinguishes the configured existing
  binding path from the setup workflow fallback that can modify IAM.
- `cloud/rc-oidc-existing-verification/`: successful exact-fc182 live OIDC, policy,
  trust and wrapping-pair verification, with complete compressed run log.
- `cloud/rc-start-fc182-dispatch/`: one authorized paid top-level dispatch and its
  initial run state. This does not claim procurement or remote workload success.
- `packages/pr72-delivery/`: final c8aae37 public allowlist, all 449+4 checks,
  31 real candidate cases and complete signed-repository drill success; initial
  failure remains preserved. The separately approved merge is recorded below.
- `packages/stable-post-candidate-plan/`: follow-on seal/transport preparation,
  with no production activation or external writes.
- `cloud-authorization-and-candidate-followup-archive.json`: original paths,
  sizes and digests for these public allowlist copies.

- `cloud/bundle-fc182/`: complete exact-source bundle verification, all payload
  member digests and reproducible verifier; first read-only download failure
  retained. Large binary ZIP and private material are excluded.

- `packages/pr72-merge/`: exact user-approved rebase merge, Git tree proof and
  retained clean worktree decision; failed read-only GETs remain preserved.
- `cloud/quote-fc182/`, `cloud/acquire-fc182/`: exact accepted quote and acquired
  four-host Lease, with full artifact/receipt identity checks. Actual billing and
  remote workload qualification remain separate.
- `packages/stable-seal-transport-preparation/`,
  `packages/stable-seal-transport-preparation-review/`: original scope, review
  findings and adopted five-helper production pre-write checks. Implementation
  is in progress; no actual stable seal or channel activation is claimed.
- `merge-and-cloud-acquire-archive.json`: byte-preserving allowlist copy receipts
  for these new merge, cloud-stage and scope-review records.

- `cloud/deployment-fc182/`, `cloud/rc-terminal-fc182/`: successful typed deployment,
  subsequent pre-clock failure, complete redacted log and exact zero-inventory
  proof. No measured run-start or successful rehearsal is claimed.
- `packages/storage-current-readonly/`: current official-console observations
  and public DNS/TLS, with scope limits and no resource mutation.
- `rc-terminal-and-storage-readonly-archive.json`: byte/hash allowlist copy
  receipts for the terminal cloud evidence and distribution resource inventory.

- `cloud/rc-tracking-issue/`: authorized retrospective Issue #1018 creation and
  one actual read-only preflight; original omission and first GET limitation retained.
- `evidence-fact-review/`, `cloud/issue-preflight-public-schema/`: independent
  review of the latest report changes and real public API field validation.
- `tracking-issue-and-fact-review-archive.json`: public-only copy receipts.

- `cloud/issue-preflight-repair/`, `cloud/issue-preflight-root-review/`: test-first
  exact-Issue preflight and independent source review.
- `cloud/preclock-diagnostic-repair/`, `cloud/preclock-diagnostic-independent-review/`,
  `cloud/preclock-root-validation/`, `cloud/preclock-delivery/`: reviewed PR #1019,
  preserved red cases, bounded diagnostics and local validation; Linux coverage pending.
- `packages/stable-seal-transport-implementation/`,
  `packages/stable-seal-transport-implementation-review/`,
  `packages/stable-seal-transport-delivery/`: reviewed PR #73 and all local
  generations, including the original native exit137 and the closed read race.
- `preclock-and-stable-transport-delivery-archive.json`: copy receipts for these
  implementation/review records. No production release or cloud restart occurred.

- [#73 首次完整 CI](packages/pr73-ci-generation-1/README.md)与[ready 回读](packages/pr73-ready/after.stdout)：精确候选全绿；此ready快照早于合并，后续合并收据见下方。
- [OSS/CDN资源建议](packages/storage-resource-proposal/proposal.md)：预算与资源均为建议，未执行云写入。

- [原云请求账单只读补查](cloud/provider-billing-readonly/README.md)：页面未加载费用行，实际成本仍未知。

- [#1019 首次完整 Linux CI](cloud/pr1019-ci-generation-1/README.md)与[ready回读](cloud/pr1019-ready/after.stdout)：三个平台差异子例已实际通过；此ready快照早于合并，未重新采购。

- [#73 已授权合并及后置CI](packages/pr73-merge/README.md)；[#1019 已授权合并、清理及文档自动发布](cloud/pr1019-merge/README.md)。
- [四客户端/A-B方案v2](packages/stable-public-clients-plan/generation-2/scope-proposal.md)及[独审](packages/stable-public-clients-plan-review/review.md)：历史方案，后续实现和CI状态见下方；尚未完成新native验收。
- [云续跑费用继承初稿](cloud/rc-next-execution-plan-post1019/README.md)、[方案独审](cloud/rc-next-execution-plan-review/review.md)及[第二代范围](cloud/rc-next-execution-plan-post1019/generation-2/README.md)：历史方案，后续实现和CI状态见下方；尚无实际paid续跑。
- [Go证明输入独审](cloud/released-continuation-go-review/review.md)和[consumer独审](cloud/released-continuation-consumer-review/review.md)保留；[authority首次独审](cloud/released-continuation-authority-review/review.md)两项P2已由[v3差量独审](cloud/released-continuation-authority-review/v3/review.md)关闭，85聚焦和go-vet/tidy通过；Mac完整scripts超时记录保留，最终Linux结果见下方；未执行云操作。
- [native夹具独审](packages/stable-native-clients-implementation-review/fixture-freeze-v1/review.md)和[主实现首次独审](packages/stable-native-clients-implementation-review/main-v1/review.md)保留；[v2](packages/stable-native-clients-implementation-review/main-v2/review.md)/[v3独审](packages/stable-native-clients-implementation-review/main-v3/review.md)已关闭清理/cache/覆盖缺口，未将轻量结果报告成真实49项通过。
- `continuation-and-native-first-review-archive.json`：consumer/main首次审查原字节的公共allowlist复制收据。

- [#74 首轮CI](packages/pr74-ci-generation-1/README.md)：Pages489+4及原有native job通过；Rocky manifest不可用导致stable49项0执行，首失败保留。
- [Rocky pin窄修独审](packages/mirror-pin-repair/review/review.md)及[修复身份](packages/mirror-pin-repair/06-summary.json)：该代head4ba3966的结果见下方，历史失败保留。
- [本轮公开证据复制与状态检查点](pr74-and-authority-v3-checkpoint-archive.json)：87项首次CI、10项authority v3、16项pin修复payload及各自清单，原170项与事实复核7项已逐字节核对；未发布或采购。

- [#74 第二代CI](packages/pr74-ci-generation-2/README.md)：原有native及489+4通过，stable第一个组合案例在Debian HTTPS处失败，0/49通过；清理5容器+1网络，最终零遗留。
- `packages/https-compat-repair/`、`packages/https-compat-repair-generation-2/`、`packages/https-compat-repair-generation-3/`：真实TLS及by-hash负例先红后绿，保持信任检查和客户端默认行为。
- `packages/stable-native-clients-implementation/sha512-transport-repair/`：已验签原索引的stable SHA512别名，9 composite和31旧preview planner回归；测试顺序偏差如实另记。
- [兼容性首代独审](packages/client-compatibility-review/review.md)与[差量独审](packages/client-compatibility-review/generation-2/review.md)：R1原反例保留，最终写入/客户端诊断要求通过48个独立控制，发现已关闭。
- `packages/client-compatibility-scope-v1/`、`packages/client-compatibility-final-v2/`：修复范围、499+4历史冻结、最终6源码字节及[5项补测顺序偏差](packages/client-compatibility-scope-v1/standards-deviation.md)；真实49项仍待最终head CI。
- [#1020 最终首次Linux CI](cloud/pr1020-ci-generation-1/frozen-final/README.md)：完整默认/scripts/E2E/性能/SDK/native通过、精确tree及清理已验证；Mac原超时保留，未重新采购。

- [#1020 经用户批准合并、正常清理及后置文档发布](cloud/pr1020-merge/frozen-final-v2/README.md)：e30299fa同验收tree、保护未改、primary main未动，4个CDN刷新请求受理不等于全edge完成。
- [报告事实复核](report-final-fact-review/review.md)：截至12:46Z冻结稿的128相对链接及新事实边界核对，无实质错报；后续合并接线/新CI状态另据精确收据更新。
