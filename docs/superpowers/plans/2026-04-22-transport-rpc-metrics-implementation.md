# Transport RPC Metrics Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add low-cardinality transport and RPC metrics that expose target-node congestion, dial failures, RPC latency, and timeout/error classification for cluster and node direct RPC traffic.

**Architecture:** Extend `pkg/transport` observer hooks with dial, enqueue, and RPC client events; map those events into new Prometheus metrics via `pkg/metrics` and `internal/app/observability.go`; keep existing bytes/pool metrics intact. The implementation stays infrastructure-level: transport emits only target/service/kind/result/duration signals, while app observability translates service IDs into stable names.

**Tech Stack:** Go, Prometheus client, existing `pkg/metrics` registry, `pkg/transport` pool/client abstractions, `internal/app/observability.go`, `go test`.

---

## File Structure

### Production files

- Modify: `pkg/transport/types.go`
  Responsibility: extend `ObserverHooks` with dial/enqueue/RPC event callbacks and define the new event structs.
- Modify: `pkg/transport/pool.go`
  Responsibility: emit dial and enqueue observer events with target node, kind, result, and duration.
- Modify: `pkg/transport/client.go`
  Responsibility: emit RPC client observer events with target node, service ID, result, duration, and inflight lifecycle.
- Modify: `pkg/metrics/transport.go`
  Responsibility: register and expose new transport congestion/RPC metrics.
- Modify: `pkg/metrics/registry.go`
  Responsibility: wire the expanded `TransportMetrics` fields if needed.
- Modify: `internal/app/observability.go`
  Responsibility: map transport observer events into metric calls and translate service IDs into stable service names.
- Modify: `pkg/cluster/FLOW.md`
  Responsibility: align cluster transport observability documentation if the transport observer contract changes visibly for the package.

### Test files

- Modify: `pkg/metrics/registry_test.go`
  Responsibility: assert new metric families and labels are registered and updated correctly.
- Modify: `pkg/transport/pool_test.go`
  Responsibility: assert dial/enqueue observer events for success and congestion cases.
- Modify: `pkg/transport/client_test.go`
  Responsibility: assert RPC observer events for success, timeout, cancellation, and remote-error cases.
- Modify: `internal/app/observability_test.go`
  Responsibility: assert service-ID mapping and metric recording through the new observer hooks.

## Task 1: Add failing transport metrics registry tests

**Files:**
- Modify: `pkg/metrics/registry_test.go`
- Modify: `pkg/metrics/transport.go`

- [ ] **Step 1: Write failing gather assertions for the new transport metric families**

Add tests that expect these metric families to exist and carry the requested labels:

- `wukongim_transport_rpc_client_total`
- `wukongim_transport_rpc_client_duration_seconds`
- `wukongim_transport_rpc_inflight`
- `wukongim_transport_enqueue_total`
- `wukongim_transport_dial_total`
- `wukongim_transport_dial_duration_seconds`

- [ ] **Step 2: Run the focused registry test to verify it fails**

Run: `go test ./pkg/metrics -run 'TestSlotAndTransportMetricsTrackProposalsLeaderChangesAndRPCs|TestTransportMetricsTrackRPCClientDialAndEnqueue' -count=1`

Expected: FAIL because the new metric methods and families do not exist yet.

- [ ] **Step 3: Implement the minimal `TransportMetrics` additions**

Add counters / histograms / gauges with these exact label sets:

- RPC total: `target_node`, `service`, `result`
- RPC duration: `target_node`, `service`
- RPC inflight: `target_node`, `service`
- enqueue total: `target_node`, `kind`, `result`
- dial total: `target_node`, `result`
- dial duration: `target_node`

- [ ] **Step 4: Re-run the focused registry test to verify it passes**

Run: `go test ./pkg/metrics -run 'TestSlotAndTransportMetricsTrackProposalsLeaderChangesAndRPCs|TestTransportMetricsTrackRPCClientDialAndEnqueue' -count=1`

Expected: PASS.

## Task 2: Add failing transport observer tests for dial and enqueue events

**Files:**
- Modify: `pkg/transport/types.go`
- Modify: `pkg/transport/pool.go`
- Modify: `pkg/transport/pool_test.go`

- [ ] **Step 1: Write failing tests for dial success/failure and enqueue result classification**

Cover these cases:

- dial success -> `result=ok`
- dial failure -> `result=dial_error`
- enqueue success -> `result=ok`
- queue saturation -> `result=queue_full`
- stopped pool/connection -> `result=stopped`

- [ ] **Step 2: Run the focused pool tests to verify they fail**

Run: `go test ./pkg/transport -run 'TestPoolObserverTracksDialLifecycle|TestPoolObserverTracksEnqueueResults' -count=1`

Expected: FAIL because `ObserverHooks` and pool instrumentation do not yet emit the expected events.

- [ ] **Step 3: Implement the minimal pool observer instrumentation**

Implementation rules:

- Define typed event structs in `pkg/transport/types.go`.
- Emit dial events inside `acquire()` immediately around the dial call.
- Emit enqueue events in `Send()` and `RPC()` based on the result of `writer.enqueue(...)` / acquire path classification.
- Keep existing runtime behavior identical; only observe outcomes.

- [ ] **Step 4: Re-run the focused pool tests to verify they pass**

Run: `go test ./pkg/transport -run 'TestPoolObserverTracksDialLifecycle|TestPoolObserverTracksEnqueueResults' -count=1`

Expected: PASS.

## Task 3: Add failing RPC client metrics tests

**Files:**
- Modify: `pkg/transport/client.go`
- Modify: `pkg/transport/client_test.go`

- [ ] **Step 1: Write failing tests for RPC client event classification**

Cover these cases:

- `RPCService()` success -> `result=ok`
- `context deadline exceeded` -> `result=timeout`
- `context canceled` -> `result=canceled`
- remote error -> `result=remote_error`
- queue full / stopped / other transport errors -> matching low-cardinality result buckets

Also assert inflight increments during the call and returns to zero afterward.

- [ ] **Step 2: Run the focused client tests to verify they fail**

Run: `go test ./pkg/transport -run 'TestClientRPCServiceTracksObserverEvents|TestClientRPCServiceTracksInflightGauge' -count=1`

Expected: FAIL because `Client.RPCService()` does not currently emit observer events.

- [ ] **Step 3: Implement minimal RPC client observer instrumentation**

Implementation rules:

- Instrument `Client.RPCService()` rather than only lower layers so the code has access to `serviceID`.
- Emit one begin/end lifecycle per call.
- Classify results into exactly:
  - `ok`
  - `timeout`
  - `canceled`
  - `remote_error`
  - `dial_error`
  - `queue_full`
  - `stopped`
  - `other`

- [ ] **Step 4: Re-run the focused client tests to verify they pass**

Run: `go test ./pkg/transport -run 'TestClientRPCServiceTracksObserverEvents|TestClientRPCServiceTracksInflightGauge' -count=1`

Expected: PASS.

## Task 4: Add failing app observability mapping tests

**Files:**
- Modify: `internal/app/observability.go`
- Modify: `internal/app/observability_test.go`

- [ ] **Step 1: Write failing tests for transport event -> metric mapping and service-name translation**

Cover these mappings:

- cluster services: `forward`, `controller`, `managed_slot`
- node services: `presence`, `delivery_submit`, `delivery_push`, `delivery_ack`, `delivery_offline`, `conversation_facts`, `channel_append`
- fallback: unknown service -> `service_<id>`

- [ ] **Step 2: Run the focused observability tests to verify they fail**

Run: `go test ./internal/app -run 'TestTransportMetricsObserverRecordsRPCClientMetrics|TestTransportMetricsObserverMapsServiceIDToName' -count=1`

Expected: FAIL because the observer hooks and name translation are not implemented.

- [ ] **Step 3: Implement the minimal app observability wiring**

Implementation rules:

- Keep existing `OnSend` / `OnReceive` byte metrics untouched.
- Add handling for `OnDial`, `OnEnqueue`, and `OnRPCClient`.
- Convert target node to string once in observability/metrics layer.
- Map unknown IDs to `service_<id>` without logging or panicking.

- [ ] **Step 4: Re-run the focused observability tests to verify they pass**

Run: `go test ./internal/app -run 'TestTransportMetricsObserverRecordsRPCClientMetrics|TestTransportMetricsObserverMapsServiceIDToName' -count=1`

Expected: PASS.

## Task 5: Align package flow documentation

**Files:**
- Modify: `pkg/cluster/FLOW.md`

- [ ] **Step 1: Check whether the transport observer contract described in `pkg/cluster/FLOW.md` is stale after the implementation**
- [ ] **Step 2: If stale, update the transport/startup observability bullets to mention dial/enqueue/RPC client metrics in addition to bytes/pool visibility**
- [ ] **Step 3: Review the wording to keep it architecture-level rather than implementation-noisy**

## Verification

Run the focused verification commands after implementation:

- `go test ./pkg/metrics -run 'TestSlotAndTransportMetricsTrackProposalsLeaderChangesAndRPCs|TestTransportMetricsTrackRPCClientDialAndEnqueue' -count=1`
- `go test ./pkg/transport -run 'TestPoolObserverTracksDialLifecycle|TestPoolObserverTracksEnqueueResults|TestClientRPCServiceTracksObserverEvents|TestClientRPCServiceTracksInflightGauge' -count=1`
- `go test ./internal/app -run 'TestTransportMetricsObserverRecordsRPCClientMetrics|TestTransportMetricsObserverMapsServiceIDToName' -count=1`
- `go test ./pkg/cluster -run 'TestTransportLayerWiresTransportObserver' -count=1`
