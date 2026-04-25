# Send Timeout Diagnostics Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add layered DEBUG diagnostics for send timeout analysis and a deterministic three-node integration test that reproduces quorum-wait send timeout.

**Architecture:** Keep the current send path unchanged and only add structured observability at four request boundaries: message retry, local forward decision, remote append handling, and replica quorum-wait timeout. Add one integration test that drives the existing three-node harness into a timeout state by shrinking gateway timeout while preserving `MinISR=3` metadata.

**Tech Stack:** Go, existing `wklog` structured logging, `internal/app` three-node harness, existing message/channel/node packages, `go test`.

---

## File Structure

### Production files

- Modify: `internal/usecase/message/retry.go`
  Responsibility: emit refresh-triggered and refresh-result diagnostics with metadata snapshots and retry timing.
- Modify: `internal/app/channelcluster.go`
  Responsibility: emit forward-decision and forward-result diagnostics around local append fallback.
- Modify: `internal/app/build.go`
  Responsibility: pass logger into the app channel cluster and replica factory.
- Modify: `internal/app/channelmeta.go`
  Responsibility: carry logger into replica factory construction.
- Modify: `internal/access/node/channel_append_rpc.go`
  Responsibility: emit remote append attempt/refresh/redirect diagnostics.
- Modify: `pkg/channel/replica/types.go`
  Responsibility: extend `ReplicaConfig` with an optional logger.
- Modify: `pkg/channel/replica/replica.go`
  Responsibility: store the logger on replicas.
- Modify: `pkg/channel/replica/append.go`
  Responsibility: emit a timeout snapshot when quorum wait exits via `ctx.Done()`.

### Test files

- Modify: `internal/usecase/message/logging_test.go`
  Responsibility: assert new refresh diagnostics are emitted with refreshed meta fields.
- Modify: `internal/app/channelcluster_test.go`
  Responsibility: assert forward diagnostics are emitted when a follower forwards append to leader.
- Modify: `internal/access/node/channel_append_rpc_test.go`
  Responsibility: assert remote append diagnostics are emitted across refresh/redirect paths.
- Modify: `pkg/channel/replica/append_test.go`
  Responsibility: assert timeout diagnostics are emitted on append context timeout.
- Modify: `pkg/channel/replica/testenv_test.go`
  Responsibility: wire a test logger into replica config helpers.
- Modify: `internal/app/multinode_integration_test.go`
  Responsibility: add deterministic three-node send-timeout reproduction coverage.

## Task 1: Add failing logging tests for refreshed meta and forward diagnostics

**Files:**
- Modify: `internal/usecase/message/logging_test.go`
- Modify: `internal/app/channelcluster_test.go`
- Modify: `internal/access/node/channel_append_rpc_test.go`

- [ ] **Step 1: Write failing tests for new structured log events**
- [ ] **Step 2: Run targeted tests to confirm they fail for missing diagnostics**
- [ ] **Step 3: Implement minimal logging in message retry, app forward, and node RPC layers**
- [ ] **Step 4: Re-run targeted tests to confirm they pass**

## Task 2: Add failing replica timeout diagnostic test

**Files:**
- Modify: `pkg/channel/replica/append_test.go`
- Modify: `pkg/channel/replica/testenv_test.go`
- Modify: `pkg/channel/replica/types.go`
- Modify: `pkg/channel/replica/replica.go`
- Modify: `pkg/channel/replica/append.go`

- [ ] **Step 1: Write a failing test that cancels an in-flight append and expects a timeout diagnostic snapshot**
- [ ] **Step 2: Run the targeted replica test and confirm it fails**
- [ ] **Step 3: Add the optional replica logger and timeout snapshot logging**
- [ ] **Step 4: Re-run the targeted replica test to confirm it passes**

## Task 3: Add failing three-node send-timeout reproduction test

**Files:**
- Modify: `internal/app/multinode_integration_test.go`

- [ ] **Step 1: Write a failing three-node test that shrinks gateway timeout, stops one follower, and expects send timeout semantics**
- [ ] **Step 2: Run the targeted integration test and confirm it fails or hangs for the wrong reason before the implementation is complete**
- [ ] **Step 3: Adjust the test setup to use the new diagnostics-ready path without changing send semantics**
- [ ] **Step 4: Re-run the targeted integration test to confirm deterministic timeout behavior**

## Verification

- Run: `go test ./internal/usecase/message -run 'TestSendWithMetaRefreshRetryLogsRefreshAttempt|TestSendWithMetaRefreshRetryLogsRefreshedMetaSnapshot' -count=1`
- Run: `go test ./internal/app -run 'TestAppChannelClusterAppendForwardsToLeaderWhenLocalReplicaIsFollower|TestAppChannelClusterAppendLogsForwardDiagnostics|TestThreeNodeAppGatewaySendTimesOutWaitingForQuorumWithMinISR3' -count=1`
- Run: `go test ./internal/access/node -run 'TestAppendToLeaderRPCAppendsOnTargetNode|TestAppendToLeaderRPCLogsRefreshDiagnostics' -count=1`
- Run: `go test ./pkg/channel/replica -run 'TestAppendContextCancellationReturnsPromptly|TestAppendContextCancellationLogsTimeoutSnapshot' -count=1`

