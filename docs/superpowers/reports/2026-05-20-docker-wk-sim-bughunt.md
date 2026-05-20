# Docker WK-Sim Bughunt Report

Date: 2026-05-20
Branch: `qa/wk-sim-bughunt-20260520`
Worktree: `.worktrees/wk-sim-bughunt-20260520`

## Scope

- Exercise the Docker Compose three-node development cluster plus `wk-sim`.
- Use simulated traffic to find reproducible bugs and performance issues.
- For each confirmed product issue: reproduce, fix, verify, commit, and keep searching.

## Environment Notes

- The worktree lives under the repository `.worktrees` directory. The parent `go.work` at `/Users/tt/Desktop/work/go/WuKongIM-v2/go.work` selects the original `WuKongIM` module, so Go commands in this nested worktree must use `GOWORK=off`.

## Verification Log

| Time | Command | Result | Notes |
| --- | --- | --- | --- |
| 2026-05-20 | `GOWORK=off go test ./...` | Pass | Full unit-test baseline passed in the isolated worktree. |
| 2026-05-20 | `docker compose --profile dev-sim config --quiet` | Pass | Compose configuration parsed successfully. |
| 2026-05-20 | reduced `scripts/dev-sim-compose-smoke.sh` | Pass | `WK_SIM_USERS=40`, `WK_SIM_RATE=0.5/s`, `WK_SIM_VERIFY_RECV=sampled`; transient recv errors observed during startup recovery. |
| 2026-05-20 | default `scripts/dev-sim-compose-smoke.sh --no-build` with `--timeout 180` | Pass | Reached `running` after about 107s, then emitted traffic. |
| 2026-05-20 | default `scripts/dev-sim-compose-smoke.sh --no-build --timeout 90 --skip-logs` | Fail | Timed out in `state=waiting` with `connected_users=0`, no last error, and no sent messages. |
| 2026-05-20 | `GOWORK=off go test ./scripts -run TestDevSimComposeSmokeDefaultTimeoutCoversHighTrafficStartup -count=1` | Red then pass | Regression test failed with the old 90s default and passed after increasing the default to 180s. |
| 2026-05-20 | `GOWORK=off go test ./scripts ./internal/bench/devsim -count=1` | Pass | Focused unit verification for the smoke script and dev-sim config package. |
| 2026-05-20 | default `scripts/dev-sim-compose-smoke.sh --no-build --skip-logs` | Pass | With the 180s default, fresh Compose startup reached traffic before timeout. Status still showed send errors, tracked separately as BUG-002. |
| 2026-05-20 | clean data default `scripts/dev-sim-compose-smoke.sh --no-build --skip-logs` | Pass | After removing ignored `docker/dev-cluster` / `docker/dev-sim`, the default profile reached `send_errors=0` and `recv_errors=0`. |
| 2026-05-20 | `GOWORK=off go test ./scripts -run TestDevSimComposeSmokeRejectsStatusErrorCounters -count=1` | Red then pass | Regression test proved the script used to pass when `/status` had `send_errors=2` and `recv_errors=1`; it now waits/fails instead. |
| 2026-05-20 | `GOWORK=off go test ./scripts -count=1` | Pass | Full script test suite passed after the stricter status gate. |
| 2026-05-20 | `scripts/dev-sim-compose-smoke.sh --no-up --skip-logs` | Pass | Existing clean Compose stack passed with `send_errors=0` and `recv_errors=0`. |
| 2026-05-20 | `WK_SIM_RATE=0.5/s WK_SIM_UID_PREFIX=stress-u scripts/dev-sim-compose-smoke.sh --no-build --skip-logs --timeout 180` | Fail | With the old Docker node data-plane settings, 0.5/s produced `send_errors=145` and timed out under the stricter smoke gate. |
| 2026-05-20 | `GOWORK=off go test ./docker -run TestComposeNodeConfigsUseExplicitDataPlaneConcurrency -count=1` | Red then pass | Regression test failed while node configs pinned data-plane concurrency to `1`, then passed after explicit data-plane settings were added. |
| 2026-05-20 | `WK_SIM_RATE=0.5/s WK_SIM_UID_PREFIX=stress-u2 scripts/dev-sim-compose-smoke.sh --no-build --skip-logs --timeout 180` | Pass | Clean Compose data plus explicit data-plane concurrency reached traffic with `send_errors=0` and `recv_errors=0`. |
| 2026-05-20 | `GOWORK=off go test ./docker ./scripts -count=1` | Pass | Focused regression suites passed after Docker config and smoke script changes. |
| 2026-05-20 | `WK_SIM_RATE=1/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=stress1-u scripts/dev-sim-compose-smoke.sh --no-build --skip-logs --timeout 180` | Stress limit | At roughly 1000 ingress/s target, Compose still produced send timeouts (`send_errors=257`). This is recorded as a capacity boundary, not part of the default smoke gate. |

## Findings

| ID | Status | Area | Symptom | Root Cause | Fix Commit |
| --- | --- | --- | --- | --- | --- |
| BUG-001 | Fixed | Docker dev-sim smoke | Default smoke timeout is 90s, but the Compose profile starts 1000 users with inherited `connect_rate: 10/s`; the script times out in `state=waiting` before traffic starts. | Compose overrides workload size but not simulator connect rate, so connection setup alone needs roughly 100s before the first traffic window. | `32ee9de9` |
| BUG-002 | Fixed | Docker dev-sim smoke | Smoke can report pass while `/status` has non-zero `send_errors` or `recv_errors`; this hid failures observed after dirty-data/restart runs. | The script only gated on `state=running`, `connected_users>0`, and `messages_sent>0`; it parsed neither error counter. | `00429905` |
| BUG-003 | Fixed | Docker dev-sim performance | 0.5/s stress profile timed out with `send_errors=145`; pprof during the run showed heavy RPC/storage work while Docker node configs still had data-plane pool size `1`. | Docker development configs overrode the app's higher default and did not set explicit data-plane fetch/pending limits, so high-traffic dev-sim runs were bottlenecked by a single data-plane lane. | `9dd55f80` |
| PERF-004 | Recorded | Docker dev-sim stress | 1/s stress profile with `WK_SIM_TRAFFIC_CONCURRENCY=256` still produces send timeouts on this machine. | The current Compose development profile is stable at the default 0.25/s and verified at 0.5/s after BUG-003, but 1/s is beyond the verified local capacity. | Not fixed |

## Active Test Matrix

- Compose smoke with reduced laptop-safe simulator load.
- Compose smoke with default dev-sim profile.
- Bounded stress pass with higher `WK_SIM_RATE` / `WK_SIM_TRAFFIC_CONCURRENCY`.
- Focused unit/e2e tests for every confirmed code defect.

## Continuation: main worktree Run 7

| Time | Command | Result | Notes |
| --- | --- | --- | --- |
| 2026-05-20 | `GOWORK=off go test ./pkg/channel/replica -run 'TestApplyFetch(ResultAfterSameLeaderMetaRefreshPublishesDurableLEO|StaleResultAfterMetaChangeIsFenced|TruncateResultAfterMetaChangeIsFenced)' -count=1` | Pass | Regression and stale-fence checks passed after the follower durable apply fix. |
| 2026-05-20 | `GOWORK=off go test ./pkg/channel/replica -count=1` | Pass | Full replica package verification passed. |
| 2026-05-20 | `WK_SIM_RATE=1/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=perf2-u scripts/dev-sim-compose-smoke.sh --no-build --timeout 300 --skip-logs` | Partial | Strict smoke still timed out on non-zero send errors, but diagnostics and logs no longer showed `channel: corrupt state`. |

| ID | Status | Area | Symptom | Root Cause | Fix Commit |
| --- | --- | --- | --- | --- | --- |
| BUG-008 | Fixed | Channel replica follower apply | High-rate `wk-sim` stress could produce `channel: corrupt state` after follower durable apply overlapped with same-leader metadata refresh. | The durable write was fenced before mutation, but result publication rejected any role-generation mismatch, leaving runtime LEO behind the durable log when channel key, epoch, and leader were unchanged. | Current change |

## Continuation: main worktree Run 8

| Time | Command | Result | Notes |
| --- | --- | --- | --- |
| 2026-05-20 | `GOWORK=off go test ./internal/bench/workload -run 'Test(Person|Group)WorkloadWarmupUsesWarmupDurationAsMinimumAckTimeout' -count=1` | Red then pass | Regression tests failed with `context deadline exceeded` before the warmup timeout fix and passed after it. |
| 2026-05-20 | `GOWORK=off go test ./internal/bench/workload -count=1` | Pass | Workload package verification passed. |
| 2026-05-20 | `GOWORK=off go test ./internal/bench/... ./cmd/wkbench -count=1` | Pass | Focused bench and wkbench CLI verification passed. |
| 2026-05-20 | `WK_SIM_UID_PREFIX=loop-fix8-u scripts/dev-sim-compose-smoke.sh --no-build --skip-logs --timeout 360` | Pass | Default Compose smoke reached `connected_users=1000`, `messages_sent=2239`, `send_errors=0`, `recv_errors=0` on the accumulated local cluster. |

| ID | Status | Area | Symptom | Root Cause | Fix Commit |
| --- | --- | --- | --- | --- | --- |
| BUG-009 | Fixed | wk-sim warmup | Default and high-rate Compose profiles could retry forever before `/status` reached `running` on a dirty/stressed local cluster. | Warmup used the shorter measured-run sendack/recv timeout, so cold channel activation latency could cancel the whole warmup phase before it completed. | Current change |

## Continuation: main worktree Run 9

| Time | Command | Result | Notes |
| --- | --- | --- | --- |
| 2026-05-20 | `WK_SIM_RATE=1/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=loop-fix8-stress-u scripts/dev-sim-compose-smoke.sh --no-build --skip-logs --timeout 420` | Pass | High-rate mixed profile reached `messages_sent=7034`, `send_errors=0`, `recv_errors=0` before the smoke gate exited. Continued polling reached `messages_sent=107094` with zero errors. |
| 2026-05-20 | diagnostics query after high-rate run | Pass | Cluster diagnostics returned no error events and recent node logs had no corrupt-state, timeout, append-failure, or panic lines. |
| 2026-05-20 | reduced sampled receive `scripts/dev-sim-compose-smoke.sh --no-build --skip-logs --timeout 180` | Pass | Sampled receive profile reached `messages_sent=65` at the smoke gate and `messages_sent=453` during continued polling, with zero send/recv errors. |
