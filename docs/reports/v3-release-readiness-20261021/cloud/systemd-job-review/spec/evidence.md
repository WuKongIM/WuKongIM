# Spec evidence

- Review basis: [/tmp/wukongim-v3-readiness-20261021/cloud/systemd-job-diagnosis/scope-and-failure-inventory.md](/tmp/wukongim-v3-readiness-20261021/cloud/systemd-job-diagnosis/scope-and-failure-inventory.md:9), exact HEAD `e30299fa59ea6669ea71024eabe209840f241f19`; commit list empty; 5 tracked changes + 1 new test. Six-file scope: spec lines 13–18.
- Applicable rules: repository AGENTS; digest `2220f49835b8dba20bafbc277854421222765be52e874fc6ee994bcd3ee0d746`. No applicable descendant AGENTS/FLOW in changed paths. `scripts/cloud-deployment/FLOW.md` is a sibling with package scope, not applicable. Preserve authoritative state policy / limits and test evidence; review is read-only in repository.
- Author manifest SHA `fb37e6fd2c9e58c1d3a5c64b55754991b35f3f259dba645042c9a8cad85a23d4` matches expected `fb37e6fd2c9e58c1d3a5c64b55754991b35f3f259dba645042c9a8cad85a23d4`.

## Requirement mapping

| Spec sentence / location | Evidence / conclusion |
| --- | --- |
| “explicitly present empty Job must normalize to numeric zero”; “Presence must be distinct from value.” ([spec line 9](/tmp/wukongim-v3-readiness-20261021/cloud/systemd-job-diagnosis/scope-and-failure-inventory.md:9)) | [shell lines 6–7/14/19/29–31](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-systemd-job/scripts/chat-lifecycle/classify-stage-service-state.sh:29) track presence separately; [Python lines 50–64](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-systemd-job/scripts/chat-lifecycle/pre-clock-diagnostic.py:50) hashes presence before normalization. Missing / duplicate remain invalid. |
| “Nonempty Job stays decimal uint32, max ten digits”; “leading zero is decimal, not octal.” ([spec line 9](/tmp/wukongim-v3-readiness-20261021/cloud/systemd-job-diagnosis/scope-and-failure-inventory.md:9)) | [shell lines 33–36](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-systemd-job/scripts/chat-lifecycle/classify-stage-service-state.sh:33) validates then uses base10 arithmetic; [Python line 65](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-systemd-job/scripts/chat-lifecycle/pre-clock-diagnostic.py:65) retains ASCII decimal, length and 4294967295 bounds. Independent max/overflow/10-zero/11-zero/leading88 probes pass. |
| “Process the final unterminated input line”; “Explicit shell exit 2 with no output” ([spec lines 9/25](/tmp/wukongim-v3-readiness-20261021/cloud/systemd-job-diagnosis/scope-and-failure-inventory.md:25)) | [shell lines 9–29](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-systemd-job/scripts/chat-lifecycle/classify-stage-service-state.sh:9) reads final nonempty line, checks 2-line bound/duplicates/unknown fields; [Python lines 40–72](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-systemd-job/scripts/chat-lifecycle/pre-clock-diagnostic.py:40) rejects missing/invalid/unknown. Independent no-newline reversed order and extra third-line probes pass in Bash3/5. |
| “Keep all state classifications unchanged”; “Do not change stage orchestration…deadlines…cost/permissions/workflows…” ([spec lines 9/20](/tmp/wukongim-v3-readiness-20261021/cloud/systemd-job-diagnosis/scope-and-failure-inventory.md:20)) | Shell case block [lines 38–53](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-systemd-job/scripts/chat-lifecycle/classify-stage-service-state.sh:38) byte-identical to HEAD. Active/activating/reloading are running, failed/deactivating terminal, inactive queued pending. Stage orchestrator unchanged SHA `0d202be2d48e61dccfeccf795c57cb35be427753486657a1ac7b0211d2fa3625`; no other paths touched. Diagnostic maintenance/refreshing allowance remains intentionally distinct. |
| “Empty Job remains raw SHA256(empty) while typed value is 0” ([spec line 25](/tmp/wukongim-v3-readiness-20261021/cloud/systemd-job-diagnosis/scope-and-failure-inventory.md:25)) | [Python lines 11–26/54/62–64](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-systemd-job/scripts/chat-lifecycle/pre-clock-diagnostic.py:54) digest raw bytes before conversion. Original 104B SHA `57c3e320a832417c5e803553961f261771af23813115c207218162f214488524` unchanged; Job field SHA `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`, typed Job0. Original diagnostic/unit files match terminal-public-frozen byte-for-byte. |
| “exact 104-byte initial observation reaches existing readiness deadline rather than parse failure” ([spec line 26](/tmp/wukongim-v3-readiness-20261021/cloud/systemd-job-diagnosis/scope-and-failure-inventory.md:26)) | [actual-loop test lines 181/214/259–263/283–313](/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-systemd-job/scripts/chat_lifecycle_preclock_capture_integration_test.go:297) protects exact input, real loop extraction, classifier exit, before-kill receipt, cleanup marker. Retained red tests fail original case under both shells; green passes. Bash3 original control accepted but diagnostic rejected; Bash5 original control rejected. |
| “All shipping tests written/frozen before either parser changes. Record red and green separately.” ([spec line 27](/tmp/wukongim-v3-readiness-20261021/cloud/systemd-job-diagnosis/scope-and-failure-inventory.md:27)) | tests-before-code freeze, all 8 red/green receipts and final test hashes agree. Red default 15/17 failing subcases; red loop 3 failing branches. Green default26 / loop11 branches pass per shell; 4 Darwin summary subchecks skip. Retained related suite 9 roots / 74 pass events / 4 skip per shell. Receipts/log digests verified, no re-run of heavy suite. |

## Independent bounded probe

`python3 /tmp/wukongim-v3-readiness-20261021/cloud/systemd-job-review/spec/probe.py` completed exit0; 56 cases each run against exact local Bash3 and Bash5 and Python reducer. No network, cloud, SSH, Actions, sleeps, workloads, server builds or shipping test edits. `probe-result.json` stores every input, expected state, exit/stdout/stderr and typed/hash result.

The unmodified before-kill diagnostic has start/state exits0, classifier1 and active/running, 7 seconds after readiness start. Independent replay of its derived 24B classifier input with HEAD and final script confirms Bash5 exit1→running/0, and final Python output valid=true with the same 104B digest. Original diagnosis scope does not claim to explain run32.

## Validation limits

- [/tmp/wukongim-v3-readiness-20261021/cloud/systemd-job-diagnosis/README.md](/tmp/wukongim-v3-readiness-20261021/cloud/systemd-job-diagnosis/README.md:21) and [spec line31](/tmp/wukongim-v3-readiness-20261021/cloud/systemd-job-diagnosis/scope-and-failure-inventory.md:31) explicitly reserve full Linux CI for later delivery; four Darwin cursor-summary skips remain visible. No Linux success inferred from local Bash5 binary.
- No pass here establishes a two-hour rehearsal, 72-hour formal soak, capacity or recovery acceptance.
- Findings: missing/partial = none; scope creep = none; incorrect implementation = none. These are conclusions about the approved parser fix, not approval of v3 GA.

## Six reviewed SHA256 values

| File | SHA256 |
| --- | --- |
| `scripts/chat-lifecycle/classify-stage-service-state.sh` | `86fa3c1993ea4be2e36356f1efa25810e0bf154ca74eba97eff3f5212973840a` |
| `scripts/chat-lifecycle/pre-clock-diagnostic.py` | `62d61d64faed14c8b5d0514e2280aff3ee4ef6b596c5b12630f1108830c1ef6c` |
| `scripts/chat_lifecycle_stage_state_test.go` | `ca5cd371f069eefd88832e3e71a812b6f8c435234b8ab19c75970912267aeea0` |
| `scripts/chat_lifecycle_preclock_capture_integration_test.go` | `2d68b2fe1df2aa3bfdcf85c24e33d7f0292b6d0d0c13264829d7fb10d9246a45` |
| `CHANGELOG.md` | `726e525915378e347a9a79f6bc08eaaa02ca1cee6a1ea7aec20c388bf969b561` |
| `docs/development/PROJECT_KNOWLEDGE.md` | `30bc49c6a3dc7255bfd2a9ff8e3918f7e4999e76d63de92bb8540ff5b62f74e3` |
