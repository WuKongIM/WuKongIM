# Manager Network Chart Observability Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild the manager Network page as a chart-first shadcn/Recharts dashboard backed by real local-node time-series data.

**Architecture:** Extend the existing `/manager/network/summary` response with additive history arrays built from current app collector buckets. Keep access handlers thin, copy and normalize data in `internal/usecase/management`, and render charts in `web` with a local shadcn `ChartContainer` component.

**Tech Stack:** Go manager usecase/access/app collector, React 19, Vite/Vitest, shadcn UI, Recharts.

---

## Repository Instruction Checks

Before changing code, read these flow docs and keep them consistent with the implementation:

- `internal/FLOW.md` if present before changing any `internal/*` package.
- `pkg/channel/FLOW.md` before changing `pkg/channel/transport`.
- No `FLOW.md` exists in `pkg/transport` at plan time; verify again before editing.

After implementation, decide whether the new long-poll expected-timeout classification or manager network history changes require updating either flow doc. Update docs in the same task if behavior has diverged.

---

## File Structure

- Modify: `pkg/transport/client.go`
  - Add an RPC result-classifier variant so successful RPC responses can be classified before observer emission.
- Modify: `pkg/channel/transport/transport.go`
  - Classify decoded long-poll `TimedOut` responses as `expected_timeout`.
- Modify: `internal/usecase/management/network.go`
  - Add `NetworkHistory`, `NetworkTrafficHistoryPoint`, `NetworkRPCHistoryPoint`, and `NetworkErrorHistoryPoint` structs.
  - Add `History` to `NetworkObservationSnapshot` and `NetworkSummary`.
  - Copy history in `ListNetworkSummary` without changing local-node semantics.
- Modify: `internal/app/network_observability.go`
  - Aggregate existing 100ms buckets into five-second history points for the last one-minute collector window.
  - Keep all work inside `NetworkSnapshot(now)`; no new goroutine.
- Modify: `internal/access/manager/network.go`
  - Add additive DTO fields under `history` and map usecase structs to JSON.
- Modify: `internal/access/manager/server.go` and `internal/access/manager/server_test.go`
  - Add any required interface/stub method changes only if the interface changes.
- Modify tests:
  - `internal/app/network_observability_test.go`
  - `internal/usecase/management/network_test.go`
  - `internal/access/manager/network_test.go`
- Modify web types/API/tests:
  - `web/src/lib/manager-api.types.ts`
  - `web/src/lib/manager-api.test.ts`
  - `web/src/pages/network/page.test.tsx`
  - `web/src/pages/page-shells.test.tsx` only if visible section titles change.
- Create: `web/src/components/ui/chart.tsx`
  - Local shadcn chart component wrapper for Recharts.
- Modify: `web/package.json`, `web/bun.lock`
  - Add `recharts`.
- Modify: `web/src/pages/network/page.tsx`
  - Replace list/table-first layout with chart-first sections.
- Modify: `web/src/i18n/messages/en.ts`, `web/src/i18n/messages/zh-CN.ts`
  - Add chart labels and section titles.

---

### Task 1: Long-poll Expected Timeout Classification

**Files:**
- Modify: `pkg/transport/client.go`
- Test: `pkg/transport/client_test.go`
- Modify: `pkg/channel/transport/transport.go`
- Test: `pkg/channel/transport/longpoll_session_test.go`

- [ ] **Step 1: Write failing transport classifier test**

Add `TestClientRPCServiceWithResultClassifierOverridesSuccessfulResult` in `pkg/transport/client_test.go`. It should call the new method:

```go
RPCServiceWithResultClassifier(ctx, nodeID, shardKey, serviceID, payload, classifier func(resp []byte) string)
```

Assert observer events include the start event with positive `Inflight` and a completion event with `Result == "expected_timeout"`, `Inflight == 0`, and a non-zero duration. The classifier must receive the response bytes and return `"expected_timeout"`.

- [ ] **Step 2: Run transport classifier test to verify it fails**

Run: `GOWORK=off go test ./pkg/transport -run 'TestClientRPCServiceWithResultClassifierOverridesSuccessfulResult' -count=1`
Expected: FAIL because the classifier API does not exist.

- [ ] **Step 3: Implement transport classifier method**

Add an English-commented method with this signature:

```go
func (c *Client) RPCServiceWithResultClassifier(ctx context.Context, nodeID NodeID, shardKey uint64, serviceID uint8, payload []byte, classifier func([]byte) string) ([]byte, error)
```

Rules:
- Existing `RPCService` calls the new method with `nil`.
- The classifier runs only when the underlying RPC returns `err == nil`.
- Empty classifier results fall back to `"ok"`.
- Non-nil errors still use `rpcClientResult(err)`.
- Observer start/completion inflight behavior stays unchanged.

- [ ] **Step 4: Write failing channel long-poll classification test**

Add exact tests in `pkg/channel/transport/longpoll_session_test.go`:

- `TestLongPollFetchRecordsExpectedTimeoutFromTimedOutResponse`: handler returns encoded `LongPollFetchResponse{Status: LanePollStatusOK, TimedOut: true}`; observer completion result is `expected_timeout`.
- `TestLongPollFetchRecordsDeadlineTimeoutAsAbnormalTimeout`: handler blocks until context deadline; observer completion result is `timeout`.

- [ ] **Step 5: Implement channel long-poll classifier**

Use `RPCServiceWithResultClassifier` from `Transport.LongPollFetch`. Decode the response in the classifier and return `expected_timeout` only when `TimedOut` is true; return empty string for normal successful long-poll responses so they remain `ok`.

- [ ] **Step 6: Run classification tests**

Run: `GOWORK=off go test ./pkg/transport ./pkg/channel/transport -run 'RPCServiceWithResultClassifier|LongPollFetchRecords' -count=1`
Expected: PASS.

- [ ] **Step 7: Commit classification work**

Commit: `feat: classify long-poll wait expiries`

---

### Task 2: Backend History DTOs And Collector Aggregation

**Files:**
- Modify: `internal/usecase/management/network.go`
- Modify: `internal/app/network_observability.go`
- Test: `internal/app/network_observability_test.go`
- Test: `internal/usecase/management/network_test.go`

- [ ] **Step 1: Write failing app collector history test**

Add `TestNetworkObservabilityBuildsHistoryFromBuckets` that records traffic, RPC successes, RPC abnormal timeout, queue full, dial error, remote error, and an `expected_timeout` RPC across two five-second windows. Assert `NetworkSnapshot(now).History` contains fixed-step points with tx/rx bytes, calls/success/errors/expected timeouts, and error counters.

Also add `TestNetworkObservabilityExpectedTimeoutIsNeutralServiceSample`: record `RPCClientEvent{ServiceID: 35, Result: "expected_timeout"}` and assert the service has `Calls1m == 1`, `ExpectedTimeout1m == 1`, `Success1m == 0`, `Timeout1m == 0`, `OtherError1m == 0`, and no `rpc_timeout` event.

- [ ] **Step 2: Run app collector test to verify it fails**

Run: `GOWORK=off go test ./internal/app -run 'TestNetworkObservabilityBuildsHistoryFromBuckets' -count=1`
Expected: FAIL because `History` and `expected_timeout` service handling do not exist.

- [ ] **Step 3: Add usecase history structs and copy helpers**

Add English-commented structs:
- `NetworkHistory`
- `NetworkTrafficHistoryPoint`
- `NetworkRPCHistoryPoint`
- `NetworkErrorHistoryPoint`

Add `History NetworkHistory` to `NetworkObservationSnapshot` and `NetworkSummary`. Add collector switch handling for `result == "expected_timeout"` so it increments `ExpectedTimeout1m` and not abnormal error counters.

- [ ] **Step 4: Implement collector history aggregation**

In `NetworkSnapshot(now)`, after pruning and before unlocking, build five-second buckets from existing maps:
- Window: `o.cfg.Window`
- Step: `5 * time.Second`
- Points: sorted ascending by `At`
- `traffic`: sum TX/RX bytes per step
- `rpc`: sum calls/success/errors/expected timeouts per step
- `errors`: sum dial/queue/timeout/remote-error per step

Use expected timeouts only when the result label is `expected_timeout`, which is emitted from decoded long-poll `TimedOut` responses; keep current abnormal timeout semantics unchanged.

- [ ] **Step 5: Add failing usecase copy test**

Add a test to `internal/usecase/management/network_test.go` proving `ListNetworkSummary` copies history from the local collector and still reports `scope.view == "local_node"`.

- [ ] **Step 6: Implement usecase copy behavior**

Copy `snapshot.History` into `summary.History` with defensive slice copies.

- [ ] **Step 7: Run backend tests**

Run: `GOWORK=off go test ./internal/app ./internal/usecase/management -run 'Network' -count=1`
Expected: PASS.

- [ ] **Step 8: Commit backend collector/usecase work**

Commit: `feat: add manager network history aggregates`

---

### Task 3: Manager Access DTO And Web Type Contract

**Files:**
- Modify: `internal/access/manager/network.go`
- Modify: `internal/access/manager/network_test.go`
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing access serialization test**

Extend `TestManagerNetworkSummaryReturnsLocalNodeSnapshot` expected JSON to include `history` with at least one traffic/rpc/error point.

- [ ] **Step 2: Run access test to verify it fails**

Run: `GOWORK=off go test ./internal/access/manager -run 'TestManagerNetworkSummaryReturnsLocalNodeSnapshot' -count=1`
Expected: FAIL because `history` is missing.

- [ ] **Step 3: Add access DTOs and mapping**

Add English-commented DTO structs under `NetworkSummaryResponse` and map usecase history fields to JSON.

- [ ] **Step 4: Add web TypeScript history types**

Add `history: ManagerNetworkHistory` and related point types in `web/src/lib/manager-api.types.ts`.

- [ ] **Step 5: Update manager API test fixture**

Add a `history` object to the network summary fixture in `web/src/lib/manager-api.test.ts`.

- [ ] **Step 6: Run contract tests**

Run: `GOWORK=off go test ./internal/access/manager -run 'NetworkSummary' -count=1`
Run: `cd web && bun run test -- src/lib/manager-api.test.ts`
Expected: PASS.

- [ ] **Step 7: Commit contract work**

Commit: `feat: expose manager network history`

---

### Task 4: shadcn Chart Infrastructure

**Files:**
- Create: `web/src/components/ui/chart.tsx`
- Modify: `web/package.json`
- Modify: `web/bun.lock`

- [ ] **Step 1: Install Recharts**

Run: `cd web && bun add recharts`
Expected: `package.json` and `bun.lock` update.

- [ ] **Step 2: Add shadcn chart wrapper**

Create `web/src/components/ui/chart.tsx` using shadcn's Recharts-based `ChartContainer`, `ChartTooltip`, `ChartTooltipContent`, `ChartLegend`, and `ChartLegendContent` pattern adapted to the project's React/Tailwind setup.

- [ ] **Step 3: Type-check the wrapper**

Run: `cd web && bun run build`
Expected: PASS. Do not commit chart infrastructure if the build fails.

- [ ] **Step 4: Commit chart infrastructure**

Commit: `feat: add shadcn chart primitives`

---

### Task 5: Chart-First Network Page

**Files:**
- Modify: `web/src/pages/network/page.test.tsx`
- Modify: `web/src/pages/network/page.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Modify: `web/src/pages/page-shells.test.tsx` if needed

- [ ] **Step 1: Write failing chart-first page tests**

Update `networkSummaryFixture` to include history. Add assertions for:
- `Node Health Distribution`
- `Traffic Trend`
- `Traffic by Message Type`
- `RPC Calls & Errors`
- `Peer Pool Balance`
- `Channel Data-plane`
- local-total disclaimer remains visible

Preserve existing tests for single-node cluster, source degradation, expected long-poll expiry neutrality, refresh, forbidden, and unavailable. Add tests for empty history arrays rendering a visible empty state or snapshot-total fallback. Assert key chart values and labels are visible as text outside SVG internals: TX/RX totals, expected long-poll expiries, abnormal failures, and local-total scope.

- [ ] **Step 2: Run page test to verify it fails**

Run: `cd web && bun run test -- src/pages/network/page.test.tsx`
Expected: FAIL because chart-first labels/components do not exist yet.

- [ ] **Step 3: Add i18n labels**

Add English and Chinese chart labels, legends, and empty-state strings. Preserve existing keys unless replacing visible text requires test updates.

- [ ] **Step 4: Implement derived chart data helpers**

In `page.tsx`, add pure helpers for:
- node health donut data
- pool active/idle data
- error mix data
- traffic history data
- message-type bar data
- RPC service chart data with expected timeouts separate from failures
- peer pool mini-chart data
- long-poll limit/config data

- [ ] **Step 5: Replace layout with chart-first sections**

Use `ChartContainer` and Recharts primitives for the main panels. Keep compact metric cards and detail rows below charts for exact values and accessibility.

- [ ] **Step 6: Run frontend page tests**

Run: `cd web && bun run test -- src/pages/network/page.test.tsx src/pages/page-shells.test.tsx src/app/layout/topbar.test.tsx`
Expected: PASS.

- [ ] **Step 7: Commit network page redesign**

Commit: `feat: redesign manager network page with charts`

---

### Task 6: Full Verification

**Files:**
- No intentional source edits unless verification reveals issues.

- [ ] **Step 1: Run frontend test suite**

Run: `cd web && bun run test`
Expected: PASS.

- [ ] **Step 2: Run frontend build**

Run: `cd web && bun run build`
Expected: PASS.

- [ ] **Step 3: Run relevant Go tests**

Run: `GOWORK=off go test ./pkg/transport ./pkg/channel/transport ./internal/app ./internal/usecase/management ./internal/access/manager -run 'Network|RPCServiceWithResultClassifier|LongPollFetchRecords' -count=1`
Expected: PASS.

- [ ] **Step 4: Check FLOW.md consistency**

Re-read `internal/FLOW.md` if present and `pkg/channel/FLOW.md`. If the final implementation changes documented flow, update the relevant file. If no update is needed, note why in the final summary.

- [ ] **Step 5: Check formatting and diff hygiene**

Run: `gofmt -w pkg/transport/client.go pkg/transport/client_test.go pkg/channel/transport/transport.go pkg/channel/transport/longpoll_session_test.go internal/app/network_observability.go internal/app/network_observability_test.go internal/usecase/management/network.go internal/usecase/management/network_test.go internal/access/manager/network.go internal/access/manager/network_test.go`
Run: `git diff --check`
Expected: no whitespace errors.

- [ ] **Step 6: Final commit if any verification fixes were needed**

Commit any verification-only fixes with a focused message.
