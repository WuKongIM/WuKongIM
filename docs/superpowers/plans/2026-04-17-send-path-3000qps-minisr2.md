# Send Path 3000 QPS MinISR2 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the three-node durable send path in `internal/app/send_stress_test.go` sustain `>= 3000 QPS` with `MinISR=2` while keeping `sendack` bound to durable quorum commit.

**Architecture:** First lock the benchmark contract and expose every throughput-sensitive knob through app config, CLI config, and the three-node harness so measurements are reproducible. Then move leader `ChannelStore.Append()` onto the existing cross-channel commit coordinator, preserving per-channel ordering and only publishing LEO/durable visibility after the shared Pebble sync completes; only if the official benchmark still misses target do we reduce committed-path background noise.

**Tech Stack:** Go, `internal/app`, `cmd/wukongim`, Pebble, `pkg/channel/store`, `pkg/channel/replica`, `pprof`, focused `go test` suites

---

### Task 1: Lock the benchmark contract and tuning surface with failing tests

**Files:**
- Modify: `internal/app/send_stress_test.go`
- Modify: `internal/app/comm_test.go`
- Modify: `internal/app/config_test.go`
- Modify: `internal/app/lifecycle_test.go`
- Modify: `cmd/wukongim/config_test.go`

- [ ] **Step 1: Add red tests for the official throughput acceptance preset**
  Extend `TestSendStressConfigDefaultsAndOverrides` / nearby helpers in `internal/app/send_stress_test.go` so the benchmark has one named acceptance preset shared with the three-node harness, with `duration=15s`, `workers=16`, `senders=32`, `max_inflight_per_worker=64`, `ack_timeout=20s`, and a path to force `MinISR=2` instead of relying on scattered literals.

- [ ] **Step 2: Add red tests for new throughput config fields**
  In `internal/app/config_test.go` and `cmd/wukongim/config_test.go`, add coverage for `FollowerReplicationRetryInterval`, `AppendGroupCommitMaxWait`, `AppendGroupCommitMaxRecords`, `AppendGroupCommitMaxBytes`, `DataPlanePoolSize`, `DataPlaneMaxFetchInflight`, and `DataPlaneMaxPendingFetch`, including defaulting and explicit override cases.

- [ ] **Step 3: Add red build/harness wiring tests**
  In `internal/app/lifecycle_test.go` and `internal/app/comm_test.go`, add assertions that app construction forwards retry interval + append group commit overrides into the runtime/replica factory and that the three-node harness can override conservative data-plane defaults for the send-stress scenario.

- [ ] **Step 4: Run the new config-focused test slice and confirm RED**
  Run: `go test ./cmd/wukongim ./internal/app -run 'Test(SendStressConfigDefaultsAndOverrides|ConfigDefaultsSendPathTuning|ConfigPreservesExplicitSendPathTuning|LoadConfigParsesSendPathTuning|NewConfiguresSendPathTuning|ThreeNodeAppHarnessUsesSendPathTuning)' -count=1`
  Expected: `FAIL` because the new preset/config fields are not wired yet.

- [ ] **Step 5: Commit the red test contract if working in an isolated branch**
  Run: `git add internal/app/send_stress_test.go internal/app/comm_test.go internal/app/config_test.go internal/app/lifecycle_test.go cmd/wukongim/config_test.go && git commit -m "test: lock send path throughput tuning contract"`

### Task 2: Expose throughput knobs through app config, CLI parsing, and the benchmark harness

**Files:**
- Modify: `internal/app/config.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/channelmeta.go`
- Modify: `internal/app/comm_test.go`
- Modify: `internal/app/send_stress_test.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Add config fields and validation/default helpers in `internal/app/config.go`**
  Add cluster-level fields for follower retry interval and append group commit tuning, keep defaults conservative for normal app startup, explicitly preserve the current `FollowerReplicationRetryInterval=1s` default when the new field is unset, and reuse the existing data-plane helper pattern instead of inventing a parallel config path.

- [ ] **Step 2: Parse the new `WK_CLUSTER_*` keys in `cmd/wukongim/config.go`**
  Add env/conf parsing for the new tuning fields, thread them into `app.Config`, and update `cmd/wukongim/config_test.go` plus `wukongim.conf.example` so the example config stays aligned with the executable surface.

- [ ] **Step 3: Wire build-time tuning into runtime and replica creation**
  In `internal/app/build.go`, replace the hard-coded `FollowerReplicationRetryInterval: time.Second` with config-backed values that still default to `1s` when unset; in `internal/app/channelmeta.go`, pass append group commit limits into `channelreplica.ReplicaConfig` so the leader replica path uses the same tuned settings as the benchmark.

- [ ] **Step 4: Apply the official acceptance preset in the three-node stress harness**
  Update `internal/app/send_stress_test.go` and `internal/app/comm_test.go` so the benchmark path can force `MinISR=2`, higher data-plane concurrency, and the agreed throughput preset without mutating unrelated integration tests.

- [ ] **Step 5: Run the config/build test slice until GREEN**
  Run: `go test ./cmd/wukongim ./internal/app -run 'Test(SendStressConfigDefaultsAndOverrides|ConfigDefaultsSendPathTuning|ConfigPreservesExplicitSendPathTuning|LoadConfigParsesSendPathTuning|NewConfiguresSendPathTuning|ThreeNodeAppHarnessUsesSendPathTuning)' -count=1`
  Expected: `PASS`.

- [ ] **Step 6: Commit the config wiring**
  Run: `git add internal/app/config.go internal/app/build.go internal/app/channelmeta.go internal/app/comm_test.go internal/app/send_stress_test.go cmd/wukongim/config.go cmd/wukongim/config_test.go wukongim.conf.example && git commit -m "feat: expose send path throughput tuning"`

### Task 3: Lock leader append batching semantics with failing store tests

**Files:**
- Modify: `pkg/channel/store/commit_test.go`
- Create: `pkg/channel/store/logstore_commit_test.go`
- Modify: `pkg/channel/store/testenv_test.go`

- [ ] **Step 1: Extend commit coordinator tests to cover shared sync accounting**
  Reuse the existing counting/blocking Pebble test filesystem in `pkg/channel/store/commit_test.go` so there is an explicit red test for “multiple channel appends share one durable sync” instead of only testing checkpoint/apply-fetch requests.

- [ ] **Step 2: Add red `ChannelStore.Append()` semantics tests**
  Create `pkg/channel/store/logstore_commit_test.go` with focused tests for: cross-channel append batching, “sync must finish before publish/return,” “LEO stays hidden until publish completes,” and “batch failure fans out to every append waiter.”

- [ ] **Step 3: Add any small test helpers needed for multi-store setup**
  Keep helper additions in `pkg/channel/store/testenv_test.go` tiny and store-specific so tests can create multiple `ChannelStore` values against the same engine without duplicating setup boilerplate.

- [ ] **Step 4: Run the store slice and confirm RED**
  Run: `go test ./pkg/channel/store -run 'Test(CommitCoordinatorBatchesMultipleGroupsIntoSinglePebbleSync|CommitCoordinatorDoesNotPublishBeforeSyncCompletes|CommitCoordinatorFanoutsBatchFailureToAllWaiters|ChannelStoreAppendUsesCommitCoordinatorAcrossChannels|ChannelStoreAppendBlocksUntilSyncCompletes|ChannelStoreAppendBatchFailureFailsAllWaiters)' -count=1`
  Expected: `FAIL` because `ChannelStore.Append()` still commits directly with `pebble.Sync`.

- [ ] **Step 5: Commit the red store contract**
  Run: `git add pkg/channel/store/commit_test.go pkg/channel/store/logstore_commit_test.go pkg/channel/store/testenv_test.go && git commit -m "test: lock channel store append batching semantics"`

### Task 4: Route leader append through the cross-channel durable commit coordinator

**Files:**
- Modify: `pkg/channel/store/logstore.go`
- Modify: `pkg/channel/store/commit.go`
- Modify: `pkg/channel/store/engine.go`
- Modify: `pkg/channel/store/channel_store.go`
- Modify: `pkg/channel/store/commit_test.go`
- Modify: `pkg/channel/store/logstore_commit_test.go`

- [ ] **Step 1: Split append into build and publish phases under the existing per-channel lock**
  Refactor `pkg/channel/store/logstore.go` so base-offset allocation and record encoding stay under `writeMu`, and keep `writeInProgress` plus `LEO()` visibility pinned to the pre-append state until the coordinator publish callback runs after a successful sync; only then should `recordDurableCommit`, `leo.Store`, `loaded.Store`, and `writeInProgress=false` become visible.

- [ ] **Step 2: Reuse the existing commit coordinator for synced appends**
  Update `pkg/channel/store/logstore.go` / `pkg/channel/store/commit.go` so synced `Append()` submits a `commitRequest` to the engine coordinator, while zero-record and explicit no-sync paths still bypass the coordinator safely.

- [ ] **Step 3: Preserve failure and shutdown behavior**
  Ensure `pkg/channel/store/engine.go` and `pkg/channel/store/channel_store.go` keep close semantics intact, fan out commit failures to every waiting append, and never publish partial durable state when the shared batch fails or the engine is closing.

- [ ] **Step 4: Run the store test slice until GREEN**
  Run: `go test ./pkg/channel/store -run 'Test(CommitCoordinator|ChannelStoreAppend)' -count=1`
  Expected: `PASS`.

- [ ] **Step 5: Run the broader store package as a regression check**
  Run: `go test ./pkg/channel/store -count=1`
  Expected: `PASS`.

- [ ] **Step 6: Commit the durable batching implementation**
  Run: `git add pkg/channel/store/logstore.go pkg/channel/store/commit.go pkg/channel/store/engine.go pkg/channel/store/channel_store.go pkg/channel/store/commit_test.go pkg/channel/store/logstore_commit_test.go && git commit -m "feat: batch durable channel appends across stores"`

### Task 5: Regress durable semantics, benchmark with pprof, and align flow docs

**Files:**
- Modify: `pkg/channel/replica/append_test.go`
- Modify: `internal/app/multinode_integration_test.go`
- Modify: `internal/FLOW.md`
- Modify: `pkg/channel/FLOW.md`

- [ ] **Step 1: Add MinISR=2 regression tests above the store layer**
  In `pkg/channel/replica/append_test.go`, add a focused case proving leader append still blocks until two-of-three progress acknowledgements advance HW; in `internal/app/multinode_integration_test.go`, add or update a durable send test so app-level `sendack` still waits for durable commit with `MinISR=2`.

- [ ] **Step 2: Run the new replica/app regression slice and confirm RED before implementation touches if needed**
  Run: `go test ./pkg/channel/replica ./internal/app -run 'Test(AppendWaitsUntilMinISRTwoReplicasAcknowledge|ThreeNodeAppGatewaySendUsesDurableCommitWithMinISR2)' -count=1`
  Expected: `FAIL` if the new tests expose a remaining semantic gap; otherwise keep the added coverage and proceed.

- [ ] **Step 3: Run the targeted regression suites until GREEN**
  Run: `go test ./pkg/channel/replica ./pkg/channel/runtime ./internal/app ./cmd/wukongim -run 'Test(Append|ApplyProgressAck|SessionReplicationRetryIntervalUsesConfigOverride|ThreeNodeAppGatewaySend|ThreeNodeAppSendAckSurvivesLeaderCrash|ThreeNodeAppHarnessUsesExplicitDataPlaneConcurrency|LoadConfig|NewConfiguresISRMaxFetchInflightPeer)' -count=1`
  Expected: `PASS`.

- [ ] **Step 4: Run the official acceptance benchmark and capture profiles**
  Run:
  ```bash
  mkdir -p tmp/profiles
  WK_SEND_STRESS=1 \
  WK_SEND_STRESS_MODE=throughput \
  WK_SEND_STRESS_DURATION=15s \
  WK_SEND_STRESS_WORKERS=16 \
  WK_SEND_STRESS_SENDERS=32 \
  WK_SEND_STRESS_MAX_INFLIGHT_PER_WORKER=64 \
  WK_SEND_STRESS_ACK_TIMEOUT=20s \
  go test ./internal/app -run TestSendStressThreeNode -count=1 -timeout 20m \
    -cpuprofile=tmp/profiles/send-stress.cpu.out \
    -blockprofile=tmp/profiles/send-stress.block.out
  go tool pprof -top tmp/profiles/send-stress.cpu.out
  go tool pprof -top tmp/profiles/send-stress.block.out
  ```
  Expected: test log reports `qps >= 3000`, `success == total`, `error_rate == 0`, `verification_failures == 0`, and the top blocking/cpu entries are no longer dominated by per-channel synced append.

- [ ] **Step 5: Update flow docs to match the final send path**
  Reflect the new config surface and “leader append uses cross-channel durable batching before publish” behavior in `internal/FLOW.md` and `pkg/channel/FLOW.md` if code changes make the current narrative stale.

- [ ] **Step 6: Commit the regression/docs/verification pass**
  Keep `tmp/profiles/*` as local verification artifacts only, summarize their key findings in the commit body or implementation notes, and run: `git add pkg/channel/replica/append_test.go internal/app/multinode_integration_test.go internal/FLOW.md pkg/channel/FLOW.md && git commit -m "test: verify 3000 qps durable send path"`

### Task 6: Conditionally reduce committed-path background noise if the benchmark still misses target

**Files:**
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/deliveryrouting_test.go`
- Modify: `internal/usecase/conversation/projector.go`
- Modify: `internal/usecase/conversation/projector_test.go`
- Modify: `internal/FLOW.md`

- [ ] **Step 1: Only start this task if Task 5 misses `3000 QPS`**
  Use the Task 5 `pprof` output to confirm that `asyncCommittedDispatcher` goroutine fan-out or `conversation.Projector` wakeup/flush contention is now a top residual cost before touching this path.

- [ ] **Step 2: Add one red test per noisy component you plan to change**
  In `internal/app/deliveryrouting_test.go`, lock bounded submission/worker behavior; in `internal/usecase/conversation/projector_test.go`, lock the reduced wakeup/flush policy that should lower contention without losing durable conversation projection.

- [ ] **Step 3: Implement the smallest bounded-noise change**
  Prefer a bounded worker or shard queue in `internal/app/deliveryrouting.go`; only tune `internal/usecase/conversation/projector.go` if profiles still point there after dispatcher changes. Do not widen scope into unrelated delivery semantics.

- [ ] **Step 4: Re-run the affected tests and the official benchmark**
  Run: `go test ./internal/app ./internal/usecase/conversation -run 'Test(AsyncCommittedDispatcher|Projector)' -count=1`
  Then rerun the Task 5 benchmark/profile command.
  Expected: tests `PASS`, benchmark reaches `>= 3000 QPS`, and profile hot spots shift away from committed-path background contention.

- [ ] **Step 5: Update docs and commit the conditional follow-up**
  Run: `git add internal/app/deliveryrouting.go internal/app/deliveryrouting_test.go internal/usecase/conversation/projector.go internal/usecase/conversation/projector_test.go internal/FLOW.md && git commit -m "perf: reduce committed path background contention"`
