# Channel Runtime Meta Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep `ChannelRuntimeMeta` lease/topology aligned after bootstrap so durable sends continue to find a writable channel leader.

**Architecture:** Extend `internal/app/channelmeta` with a narrow authoritative reconcile path that can renew expired/expiring leader leases and realign channel runtime metadata with current slot topology before local apply. Use `channelMetaSync.syncOnce()` for proactive renewal on the local channel leader node and `RefreshChannelMeta()` for on-demand recovery after send-path refresh retries.

**Tech Stack:** Go, internal/app, pkg/slot/proxy metastore, pkg/channel runtime/replica tests

---

### Task 1: Lock the missing lifecycle behavior with failing tests

**Files:**
- Modify: `internal/app/channelmeta_test.go`
- Test: `internal/app/channelmeta_test.go`

- [ ] **Step 1: Write failing tests**
- [ ] **Step 2: Run `go test ./internal/app -run 'TestChannelMetaSync(RefreshRenewsExpiredLeaderLease|SyncOnceRenewsLocalLeaderLeaseBeforeExpiry)'` and confirm RED**
- [ ] **Step 3: Commit test-only red state if working in an isolated branch**

### Task 2: Add authoritative reconcile + lease renewal logic

**Files:**
- Modify: `internal/app/channelmeta.go`
- Create: `internal/app/channelmeta_lifecycle.go`
- Modify: `internal/app/channelmeta_bootstrap.go`

- [ ] **Step 1: Add a helper that derives desired runtime metadata from current authoritative meta + slot topology**
- [ ] **Step 2: Renew lease only through authoritative `UpsertChannelRuntimeMeta`, never local-only mutation**
- [ ] **Step 3: Wire `RefreshChannelMeta()` to reconcile expired/stale meta before apply**
- [ ] **Step 4: Wire `syncOnce()` to proactively renew local-leader leases before expiry**
- [ ] **Step 5: Keep leader/channel epoch semantics explicit and minimal**

### Task 3: Verify, document, and align flow docs

**Files:**
- Modify: `internal/FLOW.md`

- [ ] **Step 1: Run targeted tests until GREEN**
- [ ] **Step 2: Update `internal/FLOW.md` to reflect proactive/on-demand lease lifecycle behavior**
- [ ] **Step 3: Re-run targeted tests after doc-adjacent code changes if any**
