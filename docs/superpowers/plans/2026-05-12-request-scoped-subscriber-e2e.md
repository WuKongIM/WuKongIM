# Request-Scoped Subscriber E2E Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a real three-node e2e scenario proving request-scoped `/message/send` subscribers deliver only to the requested online subscribers across nodes.

**Architecture:** Add one new black-box scenario package under `test/e2e/message/request_scoped_subscriber_delivery`. The package uses public HTTP API for sends and WKProto clients for receive assertions, with local helpers for request JSON, response decoding, and timeout-based non-delivery checks.

**Tech Stack:** Go e2e tests with `//go:build e2e`, `test/e2e/suite`, `net/http`, WKProto frame assertions, `testify/require`.

---

### Task 1: Add Scenario Documentation

**Files:**
- Create: `test/e2e/message/request_scoped_subscriber_delivery/AGENTS.md`
- Modify: `test/e2e/message/AGENTS.md`
- Modify: `test/e2e/AGENTS.md`

- [ ] **Step 1: Create scenario AGENTS.md**

Write the scenario purpose, cluster shape, external steps, observable outcomes, failure diagnostics, run command, and maintenance rules.

- [ ] **Step 2: Update message domain catalog**

Add `request_scoped_subscriber_delivery` to `test/e2e/message/AGENTS.md` with run command:

```bash
go test -tags=e2e ./test/e2e/message/request_scoped_subscriber_delivery -count=1
```

- [ ] **Step 3: Update root e2e catalog**

Add the same scenario to `test/e2e/AGENTS.md` catalog.

- [ ] **Step 4: Commit docs**

```bash
git add test/e2e/AGENTS.md test/e2e/message/AGENTS.md test/e2e/message/request_scoped_subscriber_delivery/AGENTS.md
git commit -m "docs: describe request scoped subscriber e2e"
```

### Task 2: Write Failing E2E Test

**Files:**
- Create: `test/e2e/message/request_scoped_subscriber_delivery/request_scoped_subscriber_delivery_test.go`

- [ ] **Step 1: Write the failing test**

Create `TestRequestScopedSubscriberDeliveryAcrossNodes` with this structure:

```go
//go:build e2e

package request_scoped_subscriber_delivery

func TestRequestScopedSubscriberDeliveryAcrossNodes(t *testing.T) {
    s := suite.New(t)
    cluster := s.StartThreeNodeCluster()
    ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
    defer cancel()

    require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

    node1 := cluster.MustNode(1)
    node2 := cluster.MustNode(2)
    node3 := cluster.MustNode(3)

    subscriberA := connectClient(t, node2, "scoped-subscriber-a", "scoped-subscriber-a-device", cluster)
    subscriberB := connectClient(t, node3, "scoped-subscriber-b", "scoped-subscriber-b-device", cluster)
    outsider := connectClient(t, node1, "scoped-outsider", "scoped-outsider-device", cluster)

    apiBaseURL := "http://" + node1.Spec.APIAddr
    durable := postRequestScopedSend(t, ctx, apiBaseURL, requestScopedSendRequest{...})
    require.NotZero(t, durable.MessageID)
    require.NotZero(t, durable.MessageSeq)

    requireRequestScopedRecv(t, cluster, subscriberA, durable, ...)
    requireRequestScopedRecv(t, cluster, subscriberB, durable, ...)
    requireNoRecv(t, outsider, "scoped-outsider", cluster)

    noPersist := postRequestScopedSend(t, ctx, apiBaseURL, requestScopedSendRequest{...NoPersist: true...})
    require.NotZero(t, noPersist.MessageID)
    require.Zero(t, noPersist.MessageSeq)

    requireRequestScopedRecv(t, cluster, subscriberA, noPersist, ...)
    requireRequestScopedRecv(t, cluster, subscriberB, noPersist, ...)
    requireNoRecv(t, outsider, "scoped-outsider", cluster)
}
```

The helper assertions should check payload, sender UID, temp channel type, no `____cmd` suffix, `SyncOnce`, and optionally `NoPersist`.

- [ ] **Step 2: Run test to verify it fails before test helpers are complete or scenario exists**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/request_scoped_subscriber_delivery -run TestRequestScopedSubscriberDeliveryAcrossNodes -count=1 -v
```

Expected: FAIL before implementation is complete (new package/test initially missing helper implementation or failing if product behavior regresses).

### Task 3: Implement Test Helpers And Make Scenario Pass

**Files:**
- Modify: `test/e2e/message/request_scoped_subscriber_delivery/request_scoped_subscriber_delivery_test.go`

- [ ] **Step 1: Add local client helper**

Implement `connectClient(t, node, uid, deviceID, cluster)` using `suite.NewWKProtoClient()` and `Connect`.

- [ ] **Step 2: Add local HTTP helper**

Implement `postRequestScopedSend`:

```go
type requestScopedSendResponse struct {
    MessageID  int64  `json:"message_id"`
    MessageSeq uint64 `json:"message_seq"`
    Reason     uint8  `json:"reason"`
}
```

It should base64 encode payload, post JSON to `/message/send`, require `200`, and decode the response.

- [ ] **Step 3: Add recv assertion helper**

Implement `requireRequestScopedRecv` to read one `Recv`, check:

- `FromUID == "system-scoped"`
- `ChannelType == frame.ChannelTypeTemp`
- `!strings.Contains(ChannelID, "____cmd")`
- payload matches
- `MessageID` and `MessageSeq` match response
- `Framer.SyncOnce == true`
- `Framer.NoPersist` matches expected

Then send `RecvAck`.

- [ ] **Step 4: Add no-recv helper**

Implement `requireNoRecv` and `isTimeoutError` local to this package.

- [ ] **Step 5: Run focused e2e**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/request_scoped_subscriber_delivery -count=1 -v
```

Expected: PASS.

- [ ] **Step 6: Commit test**

```bash
git add test/e2e/message/request_scoped_subscriber_delivery/request_scoped_subscriber_delivery_test.go
git commit -m "test: cover request scoped subscriber e2e"
```

### Task 4: Verification

**Files:**
- All changed files

- [ ] **Step 1: Run focused e2e again**

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/request_scoped_subscriber_delivery -count=1 -v
```

Expected: PASS.

- [ ] **Step 2: Run supporting smoke**

```bash
GOWORK=off go test -tags=e2e ./test/e2e/suite -count=1
GOWORK=off go test ./internal/access/api ./internal/usecase/message ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 3: Check diff hygiene**

```bash
git diff --check
git status --short --branch
```

Expected: no whitespace errors; only expected committed branch state.
