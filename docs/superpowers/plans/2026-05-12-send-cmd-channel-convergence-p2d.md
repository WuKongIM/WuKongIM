# Send CMD Channel Convergence P2d Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore P2d-a/P2d-b CMD channel semantics: source-channel business authority, hardened subscriber/tag fences, and realtime online delivery for command-style `NoPersist` sends.

**Architecture:** Keep access adapters thin and keep send orchestration in `internal/usecase/message`. CMD channel write/delivery identity stays `source____cmd`, while permissions and subscribers resolve from the source channel through `internal/usecase/message` and `internal/usecase/delivery`. Non-durable CMD messages bypass channel log append but still use the delivery runtime through `delivery.App.SubmitRealtime`, preserving cross-node online fanout through existing app/runtime routing.

**Tech Stack:** Go, `testing` + `github.com/stretchr/testify/require`, WuKongIM internal usecases/runtime, `GOWORK=off go test`.

---

## Spec Reference

- Design spec: `docs/superpowers/specs/2026-05-12-send-cmd-channel-convergence-design.md`
- Business diff tracker: `docs/raw/send-path-business-logic-diff.md`
- Relevant flow doc: `internal/FLOW.md`

## Scope

Implement only P2d-a and P2d-b from the spec:

- P2d-a: harden CMD identity, source-channel permission/subscriber authority, and reusable tag fences.
- P2d-b: restore realtime online delivery for ordinary command-style `NoPersist` sends.

Do not implement P2d-c/P2d-d in this plan:

- No CMD conversation/offline sync schema changes.
- No durable request-scoped subscriber snapshot persistence.
- No sendbatch/plugin/webhook/AI/expire/AllowStranger changes.

## File Structure

- Modify `internal/usecase/message/send.go`: add ordinary non-durable CMD realtime branch and share transient realtime dispatch helper with request-scoped sends.
- Modify `internal/usecase/message/send_test.go`: add tests for `NoPersist + SyncOnce`, already-addressed CMD `NoPersist`, realtime dependency failures, and visitors CMD customer-service permission authority.
- Modify `internal/usecase/delivery/subscriber.go`: mark info-channel temporary-overlay snapshots as non-reusable unless a temp-overlay mutation fence exists; preserve existing source-channel resolution for other CMD types.
- Modify `internal/usecase/delivery/subscriber_test.go`: add resolver/tag-fence tests for agent, visitors/customer-service source versions, and info temporary overlays; extend test fake to expose temporary subscribers.
- Modify `internal/app/deliveryrouting_test.go`: add routing regression proving non-durable CMD envelopes can resolve remote session routes without command-channel log ownership.
- Modify `docs/raw/send-path-business-logic-diff.md`: record P2d-a/P2d-b completion status after implementation.
- Inspect `internal/FLOW.md`: update only if the final flow description becomes inaccurate.

## Implementation Tasks

### Task 1: Message Usecase Non-Durable CMD Realtime Path

**Files:**
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/send_test.go`

- [ ] **Step 1: Add failing tests for ordinary non-durable CMD realtime delivery**

Add these tests near the existing no-persist and SyncOnce tests in `internal/usecase/message/send_test.go`.

```go
func TestSendNoPersistWithSyncOnceDispatchesRealtimeCommandMessage(t *testing.T) {
	cluster := &fakeChannelCluster{}
	dispatcher := &recordingCommittedDispatcher{}
	realtime := &recordingRealtimeDispatcher{}
	ids := &sequenceMessageIDGenerator{next: 910}
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	permissions.members[permissionKey("g1", int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	app := New(Options{
		Now:                 fixedNowFn,
		Cluster:             cluster,
		CommittedDispatcher: dispatcher,
		RealtimeDispatcher:  realtime,
		MessageIDs:          ids,
		PermissionStore:     permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:          frame.Framer{NoPersist: true, SyncOnce: true},
		FromUID:         "u1",
		SenderSessionID: 77,
		ChannelID:       "g1",
		ChannelType:     frame.ChannelTypeGroup,
		Payload:         []byte("online cmd"),
		ClientSeq:       12,
		ClientMsgNo:     "cmd-np-1",
		Topic:           "cmd-topic",
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(910), result.MessageID)
	require.Zero(t, result.MessageSeq)
	require.Empty(t, cluster.sendRequests)
	require.Empty(t, dispatcher.calls)
	require.Len(t, realtime.calls, 1)
	call := realtime.calls[0]
	require.Equal(t, uint64(77), call.SenderSessionID)
	require.Empty(t, call.MessageScopedUIDs)
	require.Equal(t, uint64(910), call.Message.MessageID)
	require.Zero(t, call.Message.MessageSeq)
	require.Equal(t, runtimechannelid.ToCommandChannel("g1"), call.Message.ChannelID)
	require.Equal(t, frame.ChannelTypeGroup, call.Message.ChannelType)
	require.Equal(t, frame.Framer{NoPersist: true, SyncOnce: true}, call.Message.Framer)
	require.Equal(t, uint64(12), call.Message.ClientSeq)
	require.Equal(t, "cmd-np-1", call.Message.ClientMsgNo)
	require.Equal(t, "cmd-topic", call.Message.Topic)
	require.Equal(t, "u1", call.Message.FromUID)
	require.Equal(t, []byte("online cmd"), call.Message.Payload)
	require.Equal(t, int32(fixedSendNow.Unix()), call.Message.Timestamp)
}

func TestSendNoPersistAlreadyDerivedCommandDispatchesRealtimeWithoutSyncOnce(t *testing.T) {
	realtime := &recordingRealtimeDispatcher{}
	ids := &sequenceMessageIDGenerator{next: 920}
	app := New(Options{
		Now:                fixedNowFn,
		RealtimeDispatcher: realtime,
		MessageIDs:         ids,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true},
		FromUID:     "u1",
		ChannelID:   runtimechannelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("already cmd"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(920), result.MessageID)
	require.Zero(t, result.MessageSeq)
	require.Len(t, realtime.calls, 1)
	require.Equal(t, runtimechannelid.ToCommandChannel("g1"), realtime.calls[0].Message.ChannelID)
	require.Equal(t, frame.Framer{NoPersist: true}, realtime.calls[0].Message.Framer)
}
```

- [ ] **Step 2: Run the new tests and verify they fail**

Run:

```bash
GOWORK=off go test ./internal/usecase/message -run 'TestSendNoPersistWithSyncOnceDispatchesRealtimeCommandMessage|TestSendNoPersistAlreadyDerivedCommandDispatchesRealtimeWithoutSyncOnce' -count=1
```

Expected before implementation: FAIL because the existing ordinary `NoPersist` branch returns `MessageID=0`, `MessageSeq=0`, and does not call `RealtimeDispatcher`.

- [ ] **Step 3: Add failing dependency/error tests for non-durable CMD realtime**

Add these tests in `internal/usecase/message/send_test.go` near the tests from Step 1.

```go
func TestSendNoPersistCommandRequiresMessageIDGenerator(t *testing.T) {
	realtime := &recordingRealtimeDispatcher{}
	app := New(Options{
		Now:                fixedNowFn,
		RealtimeDispatcher: realtime,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true, SyncOnce: true},
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("cmd"),
	})

	require.ErrorIs(t, err, ErrMessageIDGeneratorRequired)
	require.Equal(t, SendResult{}, result)
	require.Empty(t, realtime.calls)
}

func TestSendNoPersistCommandReturnsRealtimeDispatcherError(t *testing.T) {
	wantErr := errors.New("realtime stopped")
	realtime := &recordingRealtimeDispatcher{err: wantErr}
	ids := &sequenceMessageIDGenerator{next: 930}
	app := New(Options{
		Now:                fixedNowFn,
		RealtimeDispatcher: realtime,
		MessageIDs:         ids,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true, SyncOnce: true},
		FromUID:     "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("cmd"),
	})

	require.ErrorIs(t, err, wantErr)
	require.Equal(t, SendResult{}, result)
	require.Len(t, realtime.calls, 1)
}
```

If `errors` is not already imported in `internal/usecase/message/send_test.go`, add it to the existing import block.

- [ ] **Step 4: Run the dependency/error tests and verify they fail**

Run:

```bash
GOWORK=off go test ./internal/usecase/message -run 'TestSendNoPersistCommandRequiresMessageIDGenerator|TestSendNoPersistCommandReturnsRealtimeDispatcherError' -count=1
```

Expected before implementation: FAIL because the current ordinary `NoPersist` branch returns success without checking `MessageIDs` or `RealtimeDispatcher`.

- [ ] **Step 5: Implement shared realtime dispatch in `internal/usecase/message/send.go`**

Refactor `sendRequestScopedRealtime` to use a shared helper, and call that helper from the ordinary send branch only when the message is command-style.

Change this part of `App.Send` after permission checks:

```go
if cmd.Framer.NoPersist {
	return SendResult{Reason: frame.ReasonSuccess}, nil
}
```

To this:

```go
if cmd.Framer.NoPersist {
	if cmd.Framer.SyncOnce || alreadyCommandChannel {
		realtimeCmd := cmd
		realtimeCmd.ChannelID = runtimechannelid.ToCommandChannel(cmd.ChannelID)
		return a.sendRealtime(ctx, realtimeCmd, nil)
	}
	return SendResult{Reason: frame.ReasonSuccess}, nil
}
```

Replace the body of `sendRequestScopedRealtime` with:

```go
func (a *App) sendRequestScopedRealtime(ctx context.Context, cmd SendCommand) (SendResult, error) {
	return a.sendRealtime(ctx, cmd, cmd.RequestSubscribers)
}
```

Add the shared helper below `sendRequestScopedRealtime`:

```go
// sendRealtime dispatches a transient command message without writing the channel log.
func (a *App) sendRealtime(ctx context.Context, cmd SendCommand, messageScopedUIDs []string) (SendResult, error) {
	if a.messageIDs == nil {
		return SendResult{}, ErrMessageIDGeneratorRequired
	}
	if a.realtime == nil {
		return SendResult{}, ErrRealtimeDispatcherRequired
	}
	msg := buildDurableMessage(cmd, a.now())
	msg.MessageID = a.messageIDs.Next()
	msg.MessageSeq = 0
	if err := a.realtime.SubmitRealtime(ctx, messageevents.MessageRealtime{
		Message:           msg,
		SenderSessionID:   cmd.SenderSessionID,
		MessageScopedUIDs: append([]string(nil), messageScopedUIDs...),
	}); err != nil {
		return SendResult{}, err
	}
	return SendResult{
		MessageID:  int64(msg.MessageID),
		MessageSeq: 0,
		Reason:     frame.ReasonSuccess,
	}, nil
}
```

Keep ordinary non-command `NoPersist` unchanged: it still returns success with zero IDs and no realtime dispatch.

- [ ] **Step 6: Run message usecase tests for this task**

Run:

```bash
GOWORK=off go test ./internal/usecase/message -run 'TestSendNoPersist|TestSendRequestScopedNoPersistDispatchesRealtimeScopedEnvelope|TestSendAlreadyDerived' -count=1
```

Expected after implementation: PASS.

- [ ] **Step 7: Commit Task 1**

```bash
git add internal/usecase/message/send.go internal/usecase/message/send_test.go
git commit -m "feat: route non-durable cmd sends realtime"
```

### Task 2: Message Permission Authority Regression Tests

**Files:**
- Modify: `internal/usecase/message/send_test.go`

- [ ] **Step 1: Add regression test for visitors CMD customer-service authority**

Add this test near the special-channel permission tests in `internal/usecase/message/send_test.go`.

```go
func TestSendAlreadyDerivedVisitorsChecksCustomerServicePermissionSource(t *testing.T) {
	cluster := &fakeChannelCluster{
		sendReplies: []fakeChannelClusterSendReply{
			{result: channel.AppendResult{MessageID: 940, MessageSeq: 41}},
		},
	}
	permissions := newFakePermissionStore()
	permissions.members[permissionKey("visitor1", int64(frame.ChannelTypeCustomerService))] = map[string]bool{"agent1": true}
	app := New(Options{
		Now:             fixedNowFn,
		Cluster:         cluster,
		MetaRefresher:   &fakeMetaRefresher{},
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID:     "agent1",
		ChannelID:   runtimechannelid.ToCommandChannel("visitor1"),
		ChannelType: frame.ChannelTypeVisitors,
		Payload:     []byte("visitor cmd"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, int64(940), result.MessageID)
	require.Equal(t, uint64(41), result.MessageSeq)
	require.Len(t, cluster.sendRequests, 1)
	require.Equal(t, channel.ChannelID{ID: runtimechannelid.ToCommandChannel("visitor1"), Type: frame.ChannelTypeVisitors}, cluster.sendRequests[0].ChannelID)
}
```

This test must not add `agent1` to `(visitor1, visitors)` membership. If implementation accidentally checks the visitors dimension, it returns `ReasonSubscriberNotExist` and fails.

- [ ] **Step 2: Run the regression test**

Run:

```bash
GOWORK=off go test ./internal/usecase/message -run TestSendAlreadyDerivedVisitorsChecksCustomerServicePermissionSource -count=1
```

Expected: PASS if current P2 permission convergence already satisfies the spec. If it fails, update `internal/usecase/message/permission.go` so already-derived visitors CMD sends strip to the source ID and non-self permission checks use `channelmembers.ChannelKey{ChannelID: source, ChannelType: frame.ChannelTypeCustomerService}`.

- [ ] **Step 3: Commit Task 2**

```bash
git add internal/usecase/message/send_test.go internal/usecase/message/permission.go
git commit -m "test: cover visitors cmd permission authority"
```

If `internal/usecase/message/permission.go` did not change, omit it from `git add`.

### Task 3: Delivery Subscriber Resolver and Tag Fence Hardening

**Files:**
- Modify: `internal/usecase/delivery/subscriber.go`
- Modify: `internal/usecase/delivery/subscriber_test.go`

- [ ] **Step 1: Add resolver tests for agent CMD and visitors customer-service source version**

Add these tests in `internal/usecase/delivery/subscriber_test.go` after existing command-channel subscriber tests.

```go
func TestSubscriberResolverResolvesCommandAgentFromSourceChannel(t *testing.T) {
	resolver := NewSubscriberResolver(SubscriberResolverOptions{})
	requestedID := channelid.ToCommandChannel("userA@agentB")

	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   requestedID,
		Type: frame.ChannelTypeAgent,
	})
	require.NoError(t, err)

	source := token.Source()
	require.Equal(t, SubscriberSourceKindDerived, source.Kind)
	require.Equal(t, requestedID, source.ChannelID)
	require.Equal(t, frame.ChannelTypeAgent, source.ChannelType)
	require.Equal(t, "userA@agentB", source.SourceChannelID)
	require.Equal(t, frame.ChannelTypeAgent, source.SourceChannelType)

	page, cursor, done, err := resolver.NextPage(context.Background(), token, "", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"userA", "agentB"}, page)
	require.Equal(t, "agentB", cursor)
	require.True(t, done)
}

func TestSubscriberResolverUsesCustomerServiceSourceVersionForCommandVisitors(t *testing.T) {
	store := &fakeAuthoritativeSubscriberStore{
		fakeSubscriberStore: fakeSubscriberStore{
			pageResults: []subscriberListResult{{uids: []string{"agent1"}, cursor: "agent1", done: true}},
		},
		getChannelVersions: map[subscriberSnapshotCall]uint64{
			{channelID: channelid.ToCommandChannel("visitor1"), channelType: int64(frame.ChannelTypeVisitors)}: 4,
			{channelID: "visitor1", channelType: int64(frame.ChannelTypeCustomerService)}:                      5,
		},
		permissionVersions: map[subscriberSnapshotCall]uint64{
			{channelID: "visitor1", channelType: int64(frame.ChannelTypeCustomerService)}: 9,
		},
	}
	resolver := NewSubscriberResolver(SubscriberResolverOptions{Store: store})

	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   channelid.ToCommandChannel("visitor1"),
		Type: frame.ChannelTypeVisitors,
	})
	require.NoError(t, err)

	source := token.Source()
	require.Equal(t, SubscriberSourceKindOverlayStore, source.Kind)
	require.Equal(t, channelid.ToCommandChannel("visitor1"), source.ChannelID)
	require.Equal(t, frame.ChannelTypeVisitors, source.ChannelType)
	require.Equal(t, "visitor1", source.SourceChannelID)
	require.Equal(t, frame.ChannelTypeCustomerService, source.SourceChannelType)
	require.Equal(t, uint64(4), source.SubscriberMutationVersion)
	require.Equal(t, uint64(9), source.SourceSubscriberMutationVersion)
	require.True(t, source.ReusableTagState)
	require.Contains(t, store.permissionCalls, subscriberSnapshotCall{channelID: "visitor1", channelType: int64(frame.ChannelTypeCustomerService)})
	require.NotContains(t, store.permissionCalls, subscriberSnapshotCall{channelID: "visitor1", channelType: int64(frame.ChannelTypeVisitors)})

	page, _, done, err := resolver.NextPage(context.Background(), token, "", 10)
	require.NoError(t, err)
	require.True(t, done)
	require.ElementsMatch(t, []string{"visitor1", "agent1"}, page)
	require.Equal(t, []subscriberListCall{{channelID: "visitor1", channelType: int64(frame.ChannelTypeCustomerService), afterUID: "", limit: 9}}, store.pageCalls)
}
```

- [ ] **Step 2: Run the resolver authority tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/delivery -run 'TestSubscriberResolverResolvesCommandAgentFromSourceChannel|TestSubscriberResolverUsesCustomerServiceSourceVersionForCommandVisitors' -count=1
```

Expected: PASS if current source-resolution behavior already matches the spec. If it fails, fix `internal/usecase/delivery/subscriber.go` before continuing.

- [ ] **Step 3: Extend the subscriber test fake with temporary subscriber overlay support**

In `internal/usecase/delivery/subscriber_test.go`, extend `fakeSubscriberStore`:

```go
type fakeSubscriberStore struct {
	snapshotCalls  []subscriberSnapshotCall
	pageCalls      []subscriberListCall
	temporaryCalls []subscriberSnapshotCall
	snapshotUIDs   []string
	temporaryUIDs  []string
	pageResults    []subscriberListResult
}
```

Add this method below `SnapshotChannelSubscribers`:

```go
func (f *fakeSubscriberStore) SnapshotTemporarySubscribers(_ context.Context, channelID string, channelType int64) ([]string, error) {
	f.temporaryCalls = append(f.temporaryCalls, subscriberSnapshotCall{
		channelID:   channelID,
		channelType: channelType,
	})
	return append([]string(nil), f.temporaryUIDs...), nil
}
```

This makes `NewSubscriberResolver` auto-detect the fake as `TemporarySubscriberSource`.

- [ ] **Step 4: Add failing tests for info temporary-overlay tag reusability**

Add these tests in `internal/usecase/delivery/subscriber_test.go` near the other subscriber source tests.

```go
func TestSubscriberResolverMarksCommandInfoWithTemporaryOverlayNonReusable(t *testing.T) {
	store := &fakeAuthoritativeSubscriberStore{
		fakeSubscriberStore: fakeSubscriberStore{
			temporaryUIDs: []string{"temp1"},
			pageResults:   []subscriberListResult{{uids: []string{"info1"}, cursor: "info1", done: true}},
		},
		getChannelVersions: map[subscriberSnapshotCall]uint64{
			{channelID: channelid.ToCommandChannel("infoA"), channelType: int64(frame.ChannelTypeInfo)}: 6,
			{channelID: "infoA", channelType: int64(frame.ChannelTypeInfo)}:                             7,
		},
		permissionVersions: map[subscriberSnapshotCall]uint64{
			{channelID: "infoA", channelType: int64(frame.ChannelTypeInfo)}: 8,
		},
	}
	resolver := NewSubscriberResolver(SubscriberResolverOptions{Store: store})

	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   channelid.ToCommandChannel("infoA"),
		Type: frame.ChannelTypeInfo,
	})
	require.NoError(t, err)

	source := token.Source()
	require.Equal(t, SubscriberSourceKindOverlayStore, source.Kind)
	require.Equal(t, "infoA", source.SourceChannelID)
	require.Equal(t, frame.ChannelTypeInfo, source.SourceChannelType)
	require.Equal(t, uint64(8), source.SourceSubscriberMutationVersion)
	require.False(t, source.ReusableTagState)
	require.Equal(t, []subscriberSnapshotCall{{channelID: "infoA", channelType: int64(frame.ChannelTypeInfo)}}, store.temporaryCalls)

	page, _, done, err := resolver.NextPage(context.Background(), token, "", 10)
	require.NoError(t, err)
	require.True(t, done)
	require.ElementsMatch(t, []string{"temp1", "info1"}, page)
}

func TestSubscriberResolverKeepsCommandInfoWithoutTemporaryOverlayReusable(t *testing.T) {
	store := &fakeAuthoritativeSubscriberStore{
		fakeSubscriberStore: fakeSubscriberStore{
			pageResults: []subscriberListResult{{uids: []string{"info1"}, cursor: "info1", done: true}},
		},
		getChannelVersions: map[subscriberSnapshotCall]uint64{
			{channelID: channelid.ToCommandChannel("infoA"), channelType: int64(frame.ChannelTypeInfo)}: 6,
		},
		permissionVersions: map[subscriberSnapshotCall]uint64{
			{channelID: "infoA", channelType: int64(frame.ChannelTypeInfo)}: 8,
		},
	}
	resolver := NewSubscriberResolver(SubscriberResolverOptions{Store: store})

	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   channelid.ToCommandChannel("infoA"),
		Type: frame.ChannelTypeInfo,
	})
	require.NoError(t, err)

	source := token.Source()
	require.Equal(t, SubscriberSourceKindPagedStore, source.Kind)
	require.True(t, source.ReusableTagState)
	require.Equal(t, uint64(8), source.SourceSubscriberMutationVersion)
}
```

- [ ] **Step 5: Run the info overlay tests and verify the first test fails**

Run:

```bash
GOWORK=off go test ./internal/usecase/delivery -run 'TestSubscriberResolverMarksCommandInfoWithTemporaryOverlayNonReusable|TestSubscriberResolverKeepsCommandInfoWithoutTemporaryOverlayReusable' -count=1
```

Expected before implementation: FAIL because info snapshots with temporary overlays currently keep `ReusableTagState=true`.

- [ ] **Step 6: Implement non-reusable info temporary-overlay snapshots**

In `internal/usecase/delivery/subscriber.go`, update the `frame.ChannelTypeInfo` case.

Current shape:

```go
if len(token.state.overlayUIDs) > 0 {
	token.source.Kind = SubscriberSourceKindOverlayStore
} else {
	token.source.Kind = SubscriberSourceKindPagedStore
}
```

Change it to:

```go
if len(token.state.overlayUIDs) > 0 {
	token.source.Kind = SubscriberSourceKindOverlayStore
	token.source.ReusableTagState = false
} else {
	token.source.Kind = SubscriberSourceKindPagedStore
}
```

Do not change request-scoped `ChannelTypeTemp`; it must remain `SubscriberSourceKindMessageScoped` and `ReusableTagState=false`.

- [ ] **Step 7: Run delivery resolver tests for this task**

Run:

```bash
GOWORK=off go test ./internal/usecase/delivery -run 'TestSubscriberResolver' -count=1
```

Expected after implementation: PASS.

- [ ] **Step 8: Commit Task 3**

```bash
git add internal/usecase/delivery/subscriber.go internal/usecase/delivery/subscriber_test.go
git commit -m "fix: harden cmd subscriber tag fences"
```

### Task 4: App Routing Regression for Non-Durable CMD Cross-Node Fanout

**Files:**
- Modify: `internal/app/deliveryrouting_test.go`
- Optional modify: `internal/usecase/delivery/app_test.go`

- [ ] **Step 1: Add a routing regression test for remote online routes**

Add this test near `TestLocalDeliveryResolverUsesMessageScopedSubscribers` in `internal/app/deliveryrouting_test.go`.

```go
func TestLocalDeliveryResolverRoutesNonDurableCommandGroupToRemoteSessions(t *testing.T) {
	store := &resolverSnapshotStore{
		uids: []string{"u-local", "u-remote"},
	}
	resolver := localDeliveryResolver{
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{Store: store}),
		authority: &recordingAuthoritative{batches: map[string][]presence.Route{
			"u-local":  {{UID: "u-local", NodeID: 1, BootID: 11, SessionID: 101}},
			"u-remote": {{UID: "u-remote", NodeID: 2, BootID: 22, SessionID: 202}},
		}},
		pageSize: 8,
	}

	token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{
		ChannelID:   channelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
	}, deliveryruntime.CommittedEnvelope{Message: channel.Message{
		ChannelID:   channelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
		Framer:      frame.Framer{NoPersist: true, SyncOnce: true},
		MessageID:   950,
		MessageSeq:  0,
	}})
	require.NoError(t, err)

	routes, _, done, err := resolver.ResolvePage(context.Background(), token, "", 8)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []deliveryruntime.RouteKey{
		{UID: "u-local", NodeID: 1, BootID: 11, SessionID: 101},
		{UID: "u-remote", NodeID: 2, BootID: 22, SessionID: 202},
	}, routes)
}
```

If `resolverSnapshotStore` cannot provide paged subscribers in the needed order, use the existing store fake in `internal/app/deliveryrouting_test.go` that supports `ListChannelSubscribers`, or extend the local test fake with a minimal paged implementation.

- [ ] **Step 2: Add a realtime packet-view regression for non-durable CMD**

Add this test near `TestBuildRealtimeRecvPacketStripsCommandSuffixFromClientChannelView` in `internal/app/deliveryrouting_test.go`.

```go
func TestBuildRealtimeRecvPacketStripsCommandSuffixForNonDurableCommandGroup(t *testing.T) {
	packet := buildRealtimeRecvPacket(channel.Message{
		MessageID:   950,
		MessageSeq:  0,
		Framer:      frame.Framer{NoPersist: true, SyncOnce: true},
		ChannelID:   channelid.ToCommandChannel("g1"),
		ChannelType: frame.ChannelTypeGroup,
		FromUID:     "u1",
		Payload:     []byte("online cmd"),
	}, "")

	require.Equal(t, "g1", packet.ChannelID)
	require.Equal(t, frame.ChannelTypeGroup, packet.ChannelType)
	require.True(t, packet.Framer.NoPersist)
	require.True(t, packet.Framer.SyncOnce)
	require.Zero(t, packet.MessageSeq)
}
```

This test must exercise the actual client-view conversion path, not just the delivery envelope.

- [ ] **Step 3: Run the routing and packet-view regression tests**

Run:

```bash
GOWORK=off go test ./internal/app -run 'TestLocalDeliveryResolverRoutesNonDurableCommandGroupToRemoteSessions|TestBuildRealtimeRecvPacketStripsCommandSuffixForNonDurableCommandGroup' -count=1
```

Expected: PASS if current delivery routing already treats transient envelopes the same as committed envelopes for online route resolution and client packet views already strip CMD suffixes. If it fails, fix the app-level realtime adapter, resolver wiring, or `buildRealtimeRecvPacket` so `delivery.App.SubmitRealtime` reaches the same routing path and clients see the source channel view.

- [ ] **Step 4: Add or verify `delivery.App.SubmitRealtime` coverage**

Check `internal/usecase/delivery/app_test.go`. If `TestSubmitRealtimeScopedEnvelopeDelegatesToRuntime` already verifies `SubmitRealtime` preserves `MessageScopedUIDs`, `MessageSeq=0`, `NoPersist`, and `SyncOnce`, no new test is required.

If the test is missing or incomplete, add this assertion block to that test:

```go
require.True(t, runtime.submits[0].Framer.NoPersist)
require.True(t, runtime.submits[0].Framer.SyncOnce)
require.Zero(t, runtime.submits[0].MessageSeq)
require.Equal(t, []string{"u1", "u2"}, runtime.submits[0].MessageScopedUIDs)
```

- [ ] **Step 5: Run app and delivery app tests for this task**

Run:

```bash
GOWORK=off go test ./internal/app ./internal/usecase/delivery -run 'TestLocalDeliveryResolverRoutesNonDurableCommandGroupToRemoteSessions|TestBuildRealtimeRecvPacketStripsCommandSuffixForNonDurableCommandGroup|TestSubmitRealtimeScopedEnvelopeDelegatesToRuntime' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 4**

```bash
git add internal/app/deliveryrouting_test.go internal/usecase/delivery/app_test.go
git commit -m "test: cover non-durable cmd route resolution"
```

If `internal/usecase/delivery/app_test.go` did not change, omit it from `git add`.

### Task 5: Documentation, Focused Verification, and Boundary Checks

**Files:**
- Modify: `docs/raw/send-path-business-logic-diff.md`
- Optional modify: `internal/FLOW.md`

- [ ] **Step 1: Update the raw send-path business diff document**

Append a new section to `docs/raw/send-path-business-logic-diff.md`:

```markdown
## P2d-a/P2d-b 实施后状态（2026-05-12）

本轮 CMD 频道语义收敛已恢复 / 固化以下行为：

- CMD 频道发送权限继续按 source channel 检查，durable append / delivery actor 继续使用 `source____cmd`。
- 已寻址到 `____cmd` 的输入不会重复追加后缀，person channel append key 使用规范化后的 source channel。
- group / person / agent / customer-service / visitors / info / temp CMD 订阅者解析均按 source channel 或 message-scoped snapshot 解析。
- visitors CMD 非访客本人发送时按 `(source, CustomerService)` 维度检查 denylist / subscriber / allowlist。
- info CMD 如果合并 temporary overlay，则 delivery tag 标记为 non-reusable，避免只用 info source mutation version 复用包含临时成员的 tag。
- 普通 `NoPersist + SyncOnce` 和已寻址 CMD 的 `NoPersist` 发送不写 channel log，但会分配 transient message ID 并通过 realtime delivery 投递，`MessageSeq=0`。
- 非 command-style 的普通 `NoPersist` 仍保持 P2a 行为：权限通过后返回成功，不写 durable append，也不做 realtime 投递。

仍未恢复的旧版差异包括：CMD conversation/offline sync、durable request-scoped subscribers 快照 replay 恢复、普通临时频道投递、`/message/sendbatch`、`expire`、`AllowStranger`、plugin/webhook/AI 钩子，以及特殊频道的后续副作用。
```

Keep the document concise and avoid duplicating the whole spec.

- [ ] **Step 2: Check whether `internal/FLOW.md` needs an update**

Run:

```bash
rg -n "NoPersist|SyncOnce|cmd|CMD|投递|会话" internal/FLOW.md
```

If `internal/FLOW.md` says non-durable command messages are not delivered or otherwise contradicts the new flow, update it. If it does not mention this level of detail, leave it unchanged.

- [ ] **Step 3: Run focused package tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/message ./internal/usecase/delivery ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 4: Run access adapter and command helper tests**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelid ./internal/access/api ./internal/access/gateway -count=1
```

Expected: PASS.

- [ ] **Step 5: Run internal import boundary check**

Run:

```bash
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
```

Expected: PASS.

- [ ] **Step 6: Inspect git status and ignore unrelated web admin plan**

Run:

```bash
git status --short --branch
```

Expected: only files intentionally changed by this implementation are staged or unstaged. If `docs/superpowers/plans/2026-05-12-web-admin-restructure.md` appears as untracked, leave it untouched; it is unrelated user work.

- [ ] **Step 7: Commit Task 5**

```bash
git add docs/raw/send-path-business-logic-diff.md internal/FLOW.md
git commit -m "docs: update cmd convergence status"
```

If `internal/FLOW.md` did not change, omit it from `git add`.

## Final Verification

- [ ] **Step 1: Run the combined focused verification suite**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelid ./internal/usecase/message ./internal/usecase/delivery ./internal/access/api ./internal/access/gateway ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 2: Run import boundary verification**

Run:

```bash
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
```

Expected: PASS.

- [ ] **Step 3: Review final diff**

Run:

```bash
git status --short --branch
git log --oneline -5
```

Expected:

- Branch is ahead by the new implementation commits.
- No unexpected modified files.
- Unrelated untracked web admin plan, if present, remains untouched.

## Expected Behavior After Completion

- `NoPersist + SyncOnce` ordinary channel sends check permissions, allocate transient message IDs, submit `MessageRealtime`, return `ReasonSuccess` with `MessageSeq=0`, and never append to channel log.
- Already-addressed `source____cmd` sends with `NoPersist=true` dispatch realtime even if `SyncOnce=false`.
- Ordinary non-command `NoPersist` keeps the previous P2a success-without-delivery behavior.
- CMD subscriber resolution remains source-authoritative across supported channel types.
- Request-scoped temp CMD and info temporary-overlay snapshots do not overwrite reusable channel delivery tags.
- Durable CMD conversation/offline sync remains explicitly deferred.
