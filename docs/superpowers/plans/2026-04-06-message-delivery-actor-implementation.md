# Message Delivery Actor Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split durable send from async realtime delivery, add a channel-owner virtual actor runtime with `recvack`-driven completion, and extend the system to support both personal and group-channel subscriber fanout.

**Architecture:** Keep durable writes in `internal/usecase/message`, introduce a dedicated `internal/usecase/delivery` plus `internal/runtime/delivery` stack for actor execution, and expose cross-node submit/push/ack/offline RPCs through `internal/access/node`. Group delivery will be backed by a new subscriber snapshot read path in `pkg/storage/metadb` and `pkg/storage/metastore`, while personal channels will use a codec compatible with `learn_project/WuKongIM/internal/options/common.go` so both channel kinds share one delivery pipeline.

**Tech Stack:** Go 1.23, `internal/access/gateway`, `internal/access/node`, `internal/usecase/message`, `internal/usecase/delivery`, `internal/usecase/presence`, `internal/runtime/delivery`, `internal/runtime/online`, `pkg/storage/metadb`, `pkg/storage/metafsm`, `pkg/storage/metastore`, `pkg/storage/channellog`, `pkg/cluster/raftcluster`, `testing`, `testify`.

**Spec:** `docs/superpowers/specs/2026-04-06-message-delivery-actor-design.md`

---

## Execution Notes

- Use `@superpowers/test-driven-development` on every task: failing test first, verify failure, then add the minimum implementation to pass.
- Keep the AGENTS layering intact: `access -> usecase/runtime`, `usecase -> runtime/pkg`, `app -> all`.
- Do not reintroduce synchronous post-commit fanout into `internal/usecase/message.Send`.
- Do not persist delivery runtime state in this plan. Route disconnects and node restarts still fall back to offline catch-up.
- Single-node deployment still means single-node cluster semantics; do not add local-only send shortcuts that bypass channel ownership.
- Treat personal channels and group channels as the same delivery abstraction. The only allowed difference is subscriber resolution.
- Preserve personal-channel compatibility with `learn_project/WuKongIM/internal/options/common.go:GetFakeChannelIDWith` and `GetFromUIDAndToUIDWith`. Do not replace that rule with lexical ordering or a new wire format in this plan.
- Prefer small focused files. If a file starts absorbing multiple responsibilities, split it during implementation rather than letting it grow.
- Because this harness only allows subagent spawning on explicit user request, perform a local review of this plan document before execution instead of dispatching a plan-review subagent.
- Before final handoff, run `@superpowers/verification-before-completion`.

## File Map

| Path | Responsibility |
|------|----------------|
| `internal/usecase/message/command.go` | add route-level ack/session-close commands and committed-envelope types |
| `internal/usecase/message/deps.go` | define async delivery submit/ack/offline ports |
| `internal/usecase/message/app.go` | inject delivery collaborators into the message app |
| `internal/usecase/message/send.go` | durable send only; submit committed envelope asynchronously |
| `internal/usecase/message/recvack.go` | forward `recvack` into the delivery usecase instead of no-op |
| `internal/usecase/message/send_test.go` | lock durable-send-only semantics and async submit behavior |
| `internal/usecase/message/recvack_test.go` | lock session-aware ack forwarding |
| `internal/usecase/delivery/types.go` | delivery commands, route keys, actor events, limiter settings |
| `internal/usecase/delivery/deps.go` | runtime, resolver, push, and clock interfaces |
| `internal/usecase/delivery/app.go` | delivery app construction and defaults |
| `internal/usecase/delivery/submit.go` | submit committed messages into the runtime |
| `internal/usecase/delivery/ack.go` | forward route ack notifications into actors |
| `internal/usecase/delivery/offline.go` | mark local or remote routes offline |
| `internal/usecase/delivery/app_test.go` | verify usecase wiring and nil-safe defaults |
| `internal/runtime/delivery/types.go` | actor state, inflight message, route state, mailbox event types |
| `internal/runtime/delivery/manager.go` | shard ownership, actor materialization/eviction, hotspot promotion |
| `internal/runtime/delivery/shard.go` | fixed worker loop and event dispatch |
| `internal/runtime/delivery/actor.go` | per-channel state machine, reorder buffer, page-by-page resolution |
| `internal/runtime/delivery/mailbox.go` | bounded per-actor mailbox |
| `internal/runtime/delivery/retrywheel.go` | shard-level retry scheduler with jittered backoff |
| `internal/runtime/delivery/ackindex.go` | `(session_id,message_id)` ack binding index and reverse session binding index |
| `internal/runtime/delivery/*_test.go` | actor ordering, retry, binding, eviction, hotspot, and guardrail tests |
| `internal/access/gateway/mapper.go` | include `SessionID` in `RecvAckCommand` |
| `internal/access/gateway/handler.go` | notify message usecase on session close |
| `internal/access/gateway/handler_test.go` | verify recvack/session-close forwarding and error handling |
| `internal/access/node/service_ids.go` | reserve submit/push/ack/offline RPC service IDs |
| `internal/access/node/options.go` | wire delivery collaborators into node access |
| `internal/access/node/client.go` | client helpers for delivery submit/push/ack/offline RPCs |
| `internal/access/node/delivery_submit_rpc.go` | owner submit RPC handler |
| `internal/access/node/delivery_push_rpc.go` | target-node push RPC handler with boot/session fencing |
| `internal/access/node/delivery_ack_rpc.go` | ack notify RPC handler |
| `internal/access/node/delivery_offline_rpc.go` | route-offline notify RPC handler |
| `internal/access/node/*_test.go` | round-trip, fencing, and owner-routing RPC coverage |
| `internal/usecase/presence/deps.go` | add batch endpoint query contract |
| `internal/usecase/presence/authority.go` | batch online route lookup |
| `internal/usecase/presence/authority_test.go` | batch lookup and expiry coverage |
| `internal/access/node/presence_rpc.go` | RPC codec support for batch endpoint lookup |
| `internal/access/node/presence_rpc_test.go` | batch endpoint RPC coverage |
| `internal/app/presenceauthority.go` | route batch lookups locally or remotely |
| `internal/app/messagerouting.go` | adapt presence routes and node RPCs into message/delivery collaborators |
| `internal/app/build.go` | construct the delivery runtime/usecase and wire message, node access, and gateway handler |
| `internal/app/integration_test.go` | single-node async delivery integration coverage |
| `internal/app/multinode_integration_test.go` | multi-node submit/push/ack/offline and group delivery coverage |
| `pkg/storage/metadb/catalog.go` | register subscriber table descriptor |
| `pkg/storage/metadb/subscriber.go` | metadb CRUD and page reads for channel subscribers |
| `pkg/storage/metadb/subscriber_test.go` | subscriber CRUD, paging, and snapshot coverage |
| `pkg/storage/metafsm/command.go` | encode add/remove subscriber commands |
| `pkg/storage/metafsm/statemachine.go` | apply subscriber commands into metadb |
| `pkg/storage/metafsm/state_machine_test.go` | lock subscriber command encoding and apply behavior |
| `pkg/storage/metastore/store.go` | propose subscriber mutations and authoritative subscriber reads |
| `pkg/storage/metastore/subscriber_rpc.go` | authoritative paged subscriber read RPC |
| `pkg/storage/metastore/integration_test.go` | multi-node subscriber read and mutation coverage |
| `internal/usecase/delivery/subscriber.go` | `SubscriberResolver` implementations for personal and group channels |
| `internal/usecase/delivery/personcodec.go` | canonical personal channel encode/decode helper |
| `internal/usecase/delivery/subscriber_test.go` | person codec and group snapshot resolver coverage |

## Task 1: Extract The Async Delivery Seam And Route-Level Ack Commands

**Files:**
- Modify: `internal/usecase/message/command.go`
- Modify: `internal/usecase/message/deps.go`
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/recvack.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `internal/usecase/message/recvack_test.go`
- Modify: `internal/access/gateway/mapper.go`
- Modify: `internal/access/gateway/handler.go`
- Modify: `internal/access/gateway/handler_test.go`

- [ ] **Step 1: Write the failing message and gateway tests**

Add focused coverage:

- `TestSendReturnsSuccessAfterDurableWriteAndSubmitsCommittedEnvelope`
- `TestSendDoesNotPerformSynchronousDeliveryAfterDurableWrite`
- `TestRecvAckForwardsSessionIDAndMessageIdentity`
- `TestHandlerOnSessionCloseForwardsSessionClosedCommand`

Test sketch:

```go
func TestRecvAckForwardsSessionIDAndMessageIdentity(t *testing.T) {
	app := New(Options{DeliveryAck: &recordingDeliveryAck{}})

	err := app.RecvAck(RecvAckCommand{
		UID:        "u1",
		SessionID:  42,
		MessageID:  88,
		MessageSeq: 9,
	})

	require.NoError(t, err)
	require.Equal(t, RouteAckCommand{
		UID:        "u1",
		SessionID:  42,
		MessageID:  88,
		MessageSeq: 9,
	}, app.deliveryAck.(*recordingDeliveryAck).calls[0])
}

func TestHandlerOnSessionCloseForwardsSessionClosedCommand(t *testing.T) {
	msgs := &fakeMessageUsecase{}
	handler := New(Options{Messages: msgs, Presence: &fakePresenceUsecase{}})

	err := handler.OnSessionClose(newAuthedContext(t, 7, "u7"))

	require.NoError(t, err)
	require.Equal(t, message.SessionClosedCommand{UID: "u7", SessionID: 7}, msgs.sessionClosed[0])
}
```

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test ./internal/usecase/message ./internal/access/gateway -run "TestSend|TestRecvAck|TestHandlerOnSessionClose" -count=1`

Expected: FAIL because `RecvAckCommand` has no `SessionID`, send still performs direct post-commit delivery, and session-close forwarding does not exist yet.

- [ ] **Step 3: Write the minimal seam implementation**

Define new message-layer contracts:

```go
type CommittedMessageEnvelope struct {
	ChannelID   string
	ChannelType uint8
	MessageID   uint64
	MessageSeq  uint64
	SenderUID   string
	ClientMsgNo string
	Topic       string
	Payload     []byte
	Framer      wkframe.Framer
	Setting     wkframe.Setting
	MsgKey      string
	Expire      uint32
	StreamNo    string
	ClientSeq   uint64
}

type CommittedMessageDispatcher interface {
	SubmitCommitted(ctx context.Context, env CommittedMessageEnvelope) error
}

type RouteAckCommand struct {
	UID       string
	SessionID uint64
	MessageID uint64
	MessageSeq uint64
}

type DeliveryAck interface {
	AckRoute(ctx context.Context, cmd RouteAckCommand) error
}

type SessionClosedCommand struct {
	UID       string
	SessionID uint64
}

type DeliveryOffline interface {
	SessionClosed(ctx context.Context, cmd SessionClosedCommand) error
}
```

Update send and ack behavior:

```go
sendResult := SendResult{
	MessageID:  int64(result.MessageID),
	MessageSeq: result.MessageSeq,
	Reason:     wkframe.ReasonSuccess,
}

if a.dispatcher != nil {
	_ = a.dispatcher.SubmitCommitted(ctx, committedEnvelopeFromSend(cmd, result))
}
return sendResult, nil
```

```go
func (a *App) RecvAck(cmd RecvAckCommand) error {
	if a.deliveryAck == nil {
		return nil
	}
	return a.deliveryAck.AckRoute(context.Background(), RouteAckCommand{
		UID:        cmd.UID,
		SessionID:  cmd.SessionID,
		MessageID:  uint64(cmd.MessageID),
		MessageSeq: cmd.MessageSeq,
	})
}
```

Map session ID in gateway:

```go
return message.RecvAckCommand{
	UID:        uid,
	SessionID:  ctx.Session.ID(),
	Framer:     pkt.Framer,
	MessageID:  pkt.MessageID,
	MessageSeq: pkt.MessageSeq,
}, nil
```

- [ ] **Step 4: Run the focused tests to verify they pass**

Run: `go test ./internal/usecase/message ./internal/access/gateway -run "TestSend|TestRecvAck|TestHandlerOnSessionClose" -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/message/command.go internal/usecase/message/deps.go internal/usecase/message/app.go internal/usecase/message/send.go internal/usecase/message/recvack.go internal/usecase/message/send_test.go internal/usecase/message/recvack_test.go internal/access/gateway/mapper.go internal/access/gateway/handler.go internal/access/gateway/handler_test.go
git commit -m "refactor(message): split durable send from delivery seam"
```

## Task 2: Build The Single-Node Virtual Actor Runtime

**Files:**
- Create: `internal/usecase/delivery/types.go`
- Create: `internal/usecase/delivery/deps.go`
- Create: `internal/usecase/delivery/app.go`
- Create: `internal/usecase/delivery/submit.go`
- Create: `internal/usecase/delivery/ack.go`
- Create: `internal/usecase/delivery/offline.go`
- Create: `internal/usecase/delivery/app_test.go`
- Create: `internal/runtime/delivery/types.go`
- Create: `internal/runtime/delivery/manager.go`
- Create: `internal/runtime/delivery/shard.go`
- Create: `internal/runtime/delivery/actor.go`
- Create: `internal/runtime/delivery/mailbox.go`
- Create: `internal/runtime/delivery/retrywheel.go`
- Create: `internal/runtime/delivery/ackindex.go`
- Create: `internal/runtime/delivery/manager_test.go`
- Create: `internal/runtime/delivery/actor_test.go`
- Create: `internal/runtime/delivery/retrywheel_test.go`
- Create: `internal/runtime/delivery/ackindex_test.go`

- [ ] **Step 1: Write the failing delivery runtime tests**

Add coverage for:

- `TestManagerMaterializesOneVirtualActorPerChannel`
- `TestActorBuffersOutOfOrderEnvelopeAndDispatchesInSequence`
- `TestActorBindsAckIndexOnlyForAcceptedRoutes`
- `TestActorRetryTickRetriesPendingRoutesUntilAcked`
- `TestManagerEvictsIdleActors`

Test sketch:

```go
func TestActorBuffersOutOfOrderEnvelopeAndDispatchesInSequence(t *testing.T) {
	runtime := newTestRuntime()
	channelID := delivery.EncodePersonChannel("u1", "u2")
	actor := runtime.mustActor(channelID, wkframe.ChannelTypePerson)

	actor.handle(StartDispatch{Envelope: env(seq: 2)})
	actor.handle(StartDispatch{Envelope: env(seq: 1)})

	require.Equal(t, []uint64{1, 2}, runtime.pushedSeqs())
}

func TestActorRetryTickRetriesPendingRoutesUntilAcked(t *testing.T) {
	runtime := newTestRuntime()
	channelID := delivery.EncodePersonChannel("u1", "u2")
	actor := runtime.mustActor(channelID, wkframe.ChannelTypePerson)
	runtime.pushErr = errors.New("temporary")

	actor.handle(StartDispatch{Envelope: env(seq: 1)})
	actor.handle(RetryTick{ChannelID: channelID, ChannelType: wkframe.ChannelTypePerson})

	require.GreaterOrEqual(t, runtime.pushAttemptsFor(1), 2)
}
```

- [ ] **Step 2: Run the delivery runtime tests to verify they fail**

Run: `go test ./internal/usecase/delivery ./internal/runtime/delivery -count=1`

Expected: FAIL because the delivery packages do not exist yet.

- [ ] **Step 3: Write the minimal runtime implementation**

Start with the smallest useful runtime:

```go
type Runtime interface {
	Submit(ctx context.Context, env CommittedMessageEnvelope) error
	AckRoute(ctx context.Context, cmd RouteAckCommand) error
	SessionClosed(ctx context.Context, cmd SessionClosedCommand) error
}

type Manager struct {
	shards []*Shard
	ackIdx *AckIndex
}

func (m *Manager) Submit(ctx context.Context, env CommittedMessageEnvelope) error {
	return m.shardFor(channelKey(env.ChannelID, env.ChannelType)).enqueue(StartDispatch{Envelope: env})
}
```

Actor state should only track active delivery work:

```go
type Actor struct {
	key             channellog.ChannelKey
	nextDispatchSeq uint64
	reorder         map[uint64]CommittedMessageEnvelope
	inflight        map[uint64]*InflightMessage
	lastActive      time.Time
}
```

Keep retries shard-level, not per-route goroutine:

```go
type RetryEntry struct {
	When      time.Time
	ChannelID string
	ChannelType uint8
	MessageID uint64
	Route     RouteKey
	Attempt   int
}
```

- [ ] **Step 4: Run the delivery runtime tests to verify they pass**

Run: `go test ./internal/usecase/delivery ./internal/runtime/delivery -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/delivery internal/runtime/delivery
git commit -m "feat(delivery): add single-node virtual actor runtime"
```

## Task 3: Wire Single-Node Async Delivery And Local Offline Notifications

**Files:**
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `internal/usecase/message/recvack_test.go`
- Modify: `internal/access/gateway/handler.go`
- Modify: `internal/access/gateway/handler_test.go`
- Modify: `internal/app/messagerouting.go`
- Create: `internal/app/deliveryrouting.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/integration_test.go`

- [ ] **Step 1: Write the failing single-node integration tests**

Add coverage for:

- `TestAppSendReturnsBeforeRealtimeAckArrives`
- `TestAppRecvAckCompletesLocalInflightRoute`
- `TestAppSessionCloseDropsRealtimeRouteAndDoesNotBlockSend`

Test sketch:

```go
func TestAppSendReturnsBeforeRealtimeAckArrives(t *testing.T) {
	app := newStartedTestApp(t)
	conn := connectWKProto(t, app, "u2", "d2")

	result, err := app.Message().Send(context.Background(), message.SendCommand{
		SenderUID: "u1", ChannelID: delivery.EncodePersonChannel("u1", "u2"), ChannelType: wkframe.ChannelTypePerson, Payload: []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, wkframe.ReasonSuccess, result.Reason)
	require.NotZero(t, result.MessageSeq)
	require.Eventually(t, func() bool { return conn.HasRecv(result.MessageID) }, time.Second, 10*time.Millisecond)
}
```

- [ ] **Step 2: Run the single-node tests to verify they fail**

Run: `go test ./internal/app -run "TestAppSendReturnsBeforeRealtimeAckArrives|TestAppRecvAckCompletesLocalInflightRoute|TestAppSessionCloseDropsRealtimeRouteAndDoesNotBlockSend" -count=1`

Expected: FAIL because the app does not construct or wire the delivery runtime and session-close does not notify delivery.

- [ ] **Step 3: Write the minimal single-node wiring**

Create an app adapter layer:

```go
type localDeliveryPush struct {
	online online.Registry
	ackIdx *deliveryruntime.AckIndex
	codec  *wkcodec.Codec
}

func (p *localDeliveryPush) PushBatch(ctx context.Context, cmd delivery.PushBatchCommand) (delivery.PushBatchResult, error) {
	// validate boot/session/uid/active, bind ack, write RECV, classify accepted/retryable/dropped
}
```

Wire message and delivery together in `build.go`:

```go
	app.deliveryRuntime = deliveryruntime.New(deliveryruntime.Config{...})
	app.deliveryApp = delivery.New(delivery.Options{Runtime: app.deliveryRuntime, Resolver: ..., Push: ...})
	app.messageApp = message.New(message.Options{
		Cluster:            app.channelLog,
		MetaRefresher:      app.channelMetaSync,
		CommittedDispatcher: app.deliveryApp,
		DeliveryAck:        app.deliveryApp,
		DeliveryOffline:    app.deliveryApp,
		...
	})
```

Notify message/delivery on close:

```go
func (h *Handler) OnSessionClose(ctx *coregateway.Context) error {
	var err error
	if h.messages != nil {
		err = errors.Join(err, h.messages.SessionClosed(message.SessionClosedCommand{
			UID:       uidFromContext(ctx),
			SessionID: ctx.Session.ID(),
		}))
	}
	if h.presence != nil {
		err = errors.Join(err, h.presence.Deactivate(...))
	}
	return err
}
```

- [ ] **Step 4: Run the single-node tests to verify they pass**

Run: `go test ./internal/app -run "TestAppSendReturnsBeforeRealtimeAckArrives|TestAppRecvAckCompletesLocalInflightRoute|TestAppSessionCloseDropsRealtimeRouteAndDoesNotBlockSend" -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/message internal/access/gateway/handler.go internal/access/gateway/handler_test.go internal/app/messagerouting.go internal/app/deliveryrouting.go internal/app/build.go internal/app/integration_test.go
git commit -m "feat(app): wire single-node async delivery runtime"
```

## Task 4: Add Cross-Node Submit, Push, Ack, And Offline RPCs

**Files:**
- Modify: `internal/access/node/service_ids.go`
- Modify: `internal/access/node/options.go`
- Modify: `internal/access/node/client.go`
- Create: `internal/access/node/delivery_submit_rpc.go`
- Create: `internal/access/node/delivery_submit_rpc_test.go`
- Create: `internal/access/node/delivery_push_rpc.go`
- Create: `internal/access/node/delivery_push_rpc_test.go`
- Create: `internal/access/node/delivery_ack_rpc.go`
- Create: `internal/access/node/delivery_ack_rpc_test.go`
- Create: `internal/access/node/delivery_offline_rpc.go`
- Create: `internal/access/node/delivery_offline_rpc_test.go`
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/multinode_integration_test.go`

- [ ] **Step 1: Write the failing node RPC and multinode tests**

Add coverage for:

- `TestSubmitCommittedMessageRPCRoutesToOwnerRuntime`
- `TestPushBatchRPCRejectsBootMismatchAndClosingRoutes`
- `TestAckNotifyRPCRoutesAckToOwnerActor`
- `TestRouteOfflineRPCDropsInflightRoute`
- `TestThreeNodeAppDurableSendReturnsBeforeRemoteAck`

Test sketch:

```go
func TestPushBatchRPCRejectsBootMismatchAndClosingRoutes(t *testing.T) {
	adapter, online := newTestDeliveryAdapter(t)
	online.Register(testConn("u2", sessionID: 10, bootID: 7))
	online.MarkClosing(10)

	body, err := adapter.handleDeliveryPushRPC(context.Background(), mustMarshal(t, deliveryPushRequest{
		ActorEpoch: 1,
		MessageID:  88,
		Routes: []routeTarget{{UID: "u2", NodeID: 2, BootID: 7, SessionID: 10}},
	}))

	require.NoError(t, err)
	require.Contains(t, string(body), "\"dropped\"")
}
```

- [ ] **Step 2: Run the node and multinode tests to verify they fail**

Run: `go test ./internal/access/node ./internal/app -run "TestSubmitCommittedMessageRPC|TestPushBatchRPC|TestAckNotifyRPC|TestRouteOfflineRPC|TestThreeNodeApp" -count=1`

Expected: FAIL because the submit/push/ack/offline RPC handlers and client helpers do not exist yet.

- [ ] **Step 3: Write the minimal cross-node implementation**

Reserve explicit service IDs and add RPC codecs:

```go
const (
	presenceRPCServiceID       uint8 = 5
	deliverySubmitRPCServiceID uint8 = 6
	deliveryPushRPCServiceID   uint8 = 7
	deliveryAckRPCServiceID    uint8 = 8
	deliveryOfflineRPCServiceID uint8 = 9
)
```

Client-side helpers:

```go
func (c *Client) SubmitCommitted(ctx context.Context, nodeID uint64, env delivery.CommittedMessageEnvelope) error
func (c *Client) PushBatch(ctx context.Context, nodeID uint64, cmd delivery.PushBatchCommand) (delivery.PushBatchResult, error)
func (c *Client) NotifyAck(ctx context.Context, nodeID uint64, cmd delivery.RouteAckCommand) error
func (c *Client) NotifyOffline(ctx context.Context, nodeID uint64, cmd delivery.SessionClosedCommand) error
```

Target-node push handler must bind ack before write:

```go
for _, route := range req.Routes {
	conn, ok := a.online.Connection(route.SessionID)
	if !ok || conn.UID != route.UID || conn.State != online.LocalRouteStateActive || route.BootID != a.gatewayBootID {
		resp.Dropped = append(resp.Dropped, route)
		continue
	}
	a.ackIndex.Bind(...)
	if err := conn.Session.WriteFrame(frame); err != nil {
		a.ackIndex.Unbind(...)
		resp.Retryable = append(resp.Retryable, route)
		continue
	}
	resp.Accepted = append(resp.Accepted, route)
}
```

- [ ] **Step 4: Run the node and multinode tests to verify they pass**

Run: `go test ./internal/access/node ./internal/app -run "TestSubmitCommittedMessageRPC|TestPushBatchRPC|TestAckNotifyRPC|TestRouteOfflineRPC|TestThreeNodeApp" -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/access/node internal/app/deliveryrouting.go internal/app/build.go internal/app/multinode_integration_test.go
git commit -m "feat(access/node): add cross-node delivery actor RPCs"
```

## Task 5: Add Channel Subscriber Storage And Authoritative Snapshot Reads

**Files:**
- Modify: `pkg/storage/metadb/catalog.go`
- Create: `pkg/storage/metadb/subscriber.go`
- Create: `pkg/storage/metadb/subscriber_test.go`
- Modify: `pkg/storage/metafsm/command.go`
- Modify: `pkg/storage/metafsm/statemachine.go`
- Modify: `pkg/storage/metafsm/state_machine_test.go`
- Modify: `pkg/storage/metastore/store.go`
- Create: `pkg/storage/metastore/subscriber_rpc.go`
- Modify: `pkg/storage/metastore/integration_test.go`

- [ ] **Step 1: Write the failing storage tests**

Add coverage for:

- `TestShardAddAndPageChannelSubscribers`
- `TestStateMachineAppliesAddAndRemoveSubscribers`
- `TestStoreListChannelSubscribersReadsAuthoritativeSlot`

Test sketch:

```go
func TestShardAddAndPageChannelSubscribers(t *testing.T) {
	db := openTestDB(t)
	shard := db.ForSlot(1)
	require.NoError(t, shard.AddSubscribers(context.Background(), "g1", 2, []string{"u1", "u2", "u3"}))

	page1, cursor, done, err := shard.ListSubscribersPage(context.Background(), "g1", 2, "", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"u1", "u2"}, page1)
	require.False(t, done)

	page2, _, done, err := shard.ListSubscribersPage(context.Background(), "g1", 2, cursor, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"u3"}, page2)
	require.True(t, done)
}
```

- [ ] **Step 2: Run the storage tests to verify they fail**

Run: `go test ./pkg/storage/metadb ./pkg/storage/metafsm ./pkg/storage/metastore -run "TestShardAddAndPageChannelSubscribers|TestStateMachineAppliesAddAndRemoveSubscribers|TestStoreListChannelSubscribersReadsAuthoritativeSlot" -count=1`

Expected: FAIL because the subscriber table, commands, and authoritative RPC do not exist yet.

- [ ] **Step 3: Write the minimal storage implementation**

Add a dedicated subscriber table keyed by `(channel_id, channel_type, uid)` with stable UID ordering so page reads can use the last UID as cursor.

Metadb sketch:

```go
type Subscriber struct {
	ChannelID   string
	ChannelType int64
	UID         string
}

func (s *Shard) AddSubscribers(ctx context.Context, channelID string, channelType int64, uids []string) error
func (s *Shard) RemoveSubscribers(ctx context.Context, channelID string, channelType int64, uids []string) error
func (s *Shard) ListSubscribersPage(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error)
```

Metastore authoritative read sketch:

```go
type subscriberRPCRequest struct {
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
	AfterUID    string `json:"after_uid,omitempty"`
	Limit       int    `json:"limit"`
}
```

- [ ] **Step 4: Run the storage tests to verify they pass**

Run: `go test ./pkg/storage/metadb ./pkg/storage/metafsm ./pkg/storage/metastore -run "TestShardAddAndPageChannelSubscribers|TestStateMachineAppliesAddAndRemoveSubscribers|TestStoreListChannelSubscribersReadsAuthoritativeSlot" -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/storage/metadb/catalog.go pkg/storage/metadb/subscriber.go pkg/storage/metadb/subscriber_test.go pkg/storage/metafsm/command.go pkg/storage/metafsm/statemachine.go pkg/storage/metafsm/state_machine_test.go pkg/storage/metastore/store.go pkg/storage/metastore/subscriber_rpc.go pkg/storage/metastore/integration_test.go
git commit -m "feat(storage): add channel subscriber snapshot reads"
```

## Task 6: Add Personal And Group Subscriber Resolution With Batch Presence Lookups

**Files:**
- Modify: `internal/usecase/presence/deps.go`
- Modify: `internal/usecase/presence/authority.go`
- Modify: `internal/usecase/presence/authority_test.go`
- Modify: `internal/access/node/presence_rpc.go`
- Modify: `internal/access/node/presence_rpc_test.go`
- Modify: `internal/app/presenceauthority.go`
- Modify: `internal/app/messagerouting.go`
- Create: `internal/usecase/delivery/personcodec.go`
- Create: `internal/usecase/delivery/subscriber.go`
- Create: `internal/usecase/delivery/subscriber_test.go`
- Modify: `internal/runtime/delivery/actor.go`
- Modify: `internal/runtime/delivery/actor_test.go`
- Modify: `internal/app/multinode_integration_test.go`

- [ ] **Step 1: Write the failing resolver and group-delivery tests**

Add coverage for:

- `TestPersonChannelCodecRoundTripsCanonicalIDs`
- `TestSubscriberResolverReturnsTwoUIDsForPersonChannel`
- `TestSubscriberResolverPagesGroupSubscribersFromMetastore`
- `TestPresenceAuthorityEndpointsByUIDsReturnsBatchRoutes`
- `TestActorResolvesSubscribersPageByPageAndOnlyTracksOnlineRoutes`
- `TestThreeNodeAppGroupChannelRealtimeDeliveryUsesStoredSubscribers`

Test sketch:

```go
func TestActorResolvesSubscribersPageByPageAndOnlyTracksOnlineRoutes(t *testing.T) {
	rt := newTestRuntime()
	rt.resolver.pages = [][]string{{"u2", "u3"}, {"u4"}}
	rt.routes["u2"] = []delivery.Endpoint{{NodeID: 1, BootID: 11, SessionID: 2}}
	rt.routes["u4"] = []delivery.Endpoint{{NodeID: 2, BootID: 22, SessionID: 4}}

	actor := rt.mustActor("g1", wkframe.ChannelTypeGroup)
	actor.handle(StartDispatch{Envelope: env(seq: 1, channelID: "g1", channelType: wkframe.ChannelTypeGroup)})

	require.Equal(t, []uint64{2, 4}, rt.acceptedSessionIDs(1))
	require.NotContains(t, rt.acceptedUIDs(1), "u3")
}
```

- [ ] **Step 2: Run the resolver and group-delivery tests to verify they fail**

Run: `go test ./internal/usecase/presence ./internal/usecase/delivery ./internal/runtime/delivery ./internal/access/node ./internal/app -run "TestPersonChannelCodec|TestSubscriberResolver|TestPresenceAuthorityEndpointsByUIDs|TestActorResolvesSubscribers|TestThreeNodeAppGroupChannelRealtimeDelivery" -count=1`

Expected: FAIL because there is no person-channel codec, no subscriber resolver, no batch route lookup, and the actor does not page subscribers yet.

- [ ] **Step 3: Write the minimal resolver implementation**

Add a canonical person-channel codec:

```go
func EncodePersonChannel(a, b string) string {
	aHash := wkutil.HashCrc32(a)
	bHash := wkutil.HashCrc32(b)
	if aHash > bHash {
		return a + "@" + b
	}
	return b + "@" + a
}

func DecodePersonChannel(channelID string) (string, string, error)
```

The implementation must stay compatible with:

- `learn_project/WuKongIM/internal/options/common.go:GetFakeChannelIDWith`
- `learn_project/WuKongIM/internal/options/common.go:GetFromUIDAndToUIDWith`

Keep the same `crc32`-collision fallback behavior as the existing helper for v1 compatibility.

Add unified resolver behavior:

```go
type SubscriberResolver interface {
	BeginSnapshot(ctx context.Context, key channellog.ChannelKey) (SnapshotToken, error)
	NextPage(ctx context.Context, token SnapshotToken, cursor string, limit int) ([]string, string, bool, error)
}
```

Add batch endpoint lookup so actors resolve one page of subscribers with one online query round:

```go
type BatchRecipientDirectory interface {
	EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]Endpoint, error)
}
```

Update the actor to break large group resolution into mailbox-yielding continuation events instead of scanning all subscribers in one loop.

- [ ] **Step 4: Run the resolver and group-delivery tests to verify they pass**

Run: `go test ./internal/usecase/presence ./internal/usecase/delivery ./internal/runtime/delivery ./internal/access/node ./internal/app -run "TestPersonChannelCodec|TestSubscriberResolver|TestPresenceAuthorityEndpointsByUIDs|TestActorResolvesSubscribers|TestThreeNodeAppGroupChannelRealtimeDelivery" -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/presence internal/access/node/presence_rpc.go internal/access/node/presence_rpc_test.go internal/app/presenceauthority.go internal/app/messagerouting.go internal/usecase/delivery/personcodec.go internal/usecase/delivery/subscriber.go internal/usecase/delivery/subscriber_test.go internal/runtime/delivery/actor.go internal/runtime/delivery/actor_test.go internal/app/multinode_integration_test.go
git commit -m "feat(delivery): add unified personal and group subscriber resolution"
```

## Task 7: Add Guardrails, Hotspot Isolation, And Final Verification

**Files:**
- Modify: `internal/runtime/delivery/types.go`
- Modify: `internal/runtime/delivery/manager.go`
- Modify: `internal/runtime/delivery/mailbox.go`
- Modify: `internal/runtime/delivery/retrywheel.go`
- Modify: `internal/runtime/delivery/manager_test.go`
- Modify: `internal/runtime/delivery/actor_test.go`
- Modify: `internal/runtime/delivery/retrywheel_test.go`
- Modify: `internal/app/multinode_integration_test.go`

- [ ] **Step 1: Write the failing scalability and guardrail tests**

Add coverage for:

- `TestMailboxRejectsOverDepthAndLeavesActorHealthy`
- `TestActorDropsRealtimeRoutesWhenInflightBudgetExceeded`
- `TestRetryWheelAppliesCappedBackoffWithJitter`
- `TestManagerPromotesHotChannelToDedicatedLane`
- `TestThreeNodeAppHotGroupDoesNotBlockNormalGroupDelivery`

Test sketch:

```go
func TestManagerPromotesHotChannelToDedicatedLane(t *testing.T) {
	mgr := newTestManager(Config{HotMailboxDepth: 8})
	for i := 0; i < 20; i++ {
		require.NoError(t, mgr.Submit(context.Background(), env(seq: uint64(i+1), channelID: "g-hot")))
	}
	require.Eventually(t, func() bool { return mgr.ActorLane("g-hot", wkframe.ChannelTypeGroup) == deliveryruntime.LaneDedicated }, time.Second, 10*time.Millisecond)
}
```

- [ ] **Step 2: Run the scalability tests to verify they fail**

Run: `go test ./internal/runtime/delivery ./internal/app -run "TestMailboxRejectsOverDepth|TestActorDropsRealtimeRoutesWhenInflightBudgetExceeded|TestRetryWheelAppliesCappedBackoffWithJitter|TestManagerPromotesHotChannelToDedicatedLane|TestThreeNodeAppHotGroupDoesNotBlockNormalGroupDelivery" -count=1`

Expected: FAIL because mailbox limits, route budgets, and dedicated-lane hotspot promotion are not implemented yet.

- [ ] **Step 3: Write the minimal guardrail implementation**

Add explicit limits and downgrade behavior:

```go
type Limits struct {
	SubscriberPageSize          int
	PresenceBatchSize           int
	MaxInflightMessagesPerActor int
	MaxInflightRoutesPerActor   int
	MaxMailboxDepth             int
	RealtimeRetryMaxAge         time.Duration
	MaxRetryAttempts            int
	HotMailboxDepth             int
}
```

Hot channel promotion should stay local to the owner node:

```go
if actor.mailboxDepth() >= cfg.HotMailboxDepth && actor.lane == LaneShared {
	m.promoteToDedicated(actor.key)
}
```

Dropping realtime work must never fail durable send:

```go
if actor.inflightRouteCount >= limits.MaxInflightRoutesPerActor {
	route.State = RouteStateDropped
	route.DropReason = "realtime_budget_exceeded"
	return
}
```

- [ ] **Step 4: Run the focused scalability tests and the full related verification suite**

Run: `go test ./internal/runtime/delivery ./internal/usecase/delivery ./internal/usecase/message ./internal/usecase/presence ./internal/access/node ./internal/access/gateway ./internal/app ./pkg/storage/metadb ./pkg/storage/metafsm ./pkg/storage/metastore -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/delivery internal/app/multinode_integration_test.go
git commit -m "feat(delivery): add guardrails and hotspot isolation"
```

## Final Verification

- [ ] Run `go test ./internal/runtime/delivery ./internal/usecase/delivery ./internal/usecase/message ./internal/usecase/presence ./internal/access/node ./internal/access/gateway ./internal/app ./pkg/storage/metadb ./pkg/storage/metafsm ./pkg/storage/metastore -count=1`
- [ ] Run `go test ./...`
- [ ] Inspect `git status --short` and confirm only intended files changed.
- [ ] Summarize any residual risks:
  - personal channel `crc32` collision compatibility
  - large-group snapshot read costs
  - non-persistent delivery state after owner restart
