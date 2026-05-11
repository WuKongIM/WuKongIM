# Send NoPersist P2a Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore the legacy-compatible `NoPersist` non-durable send boundary for the current `/message/send` and message usecase path.

**Architecture:** Keep all validation and P0/P1 business permissions in `internal/usecase/message` before the shortcut. When `cmd.Framer.NoPersist` is true and permissions pass, return a successful `SendResult` without requiring channel cluster dependencies, without channel-log append, and without committed-message dispatch. The HTTP adapter only maps the legacy `header.no_persist` request flag into `message.SendCommand.Framer`.

**Tech Stack:** Go, Gin HTTP API, existing `frame.Framer`, existing `message.App.Send`, TDD with `GOWORK=off go test` in the dedicated worktree.

---

## Reference Docs

- Design: `docs/superpowers/specs/2026-05-11-send-nopersist-p2a-design.md`
- Difference analysis: `docs/raw/send-path-business-logic-diff.md`
- Previous P0 plan: `docs/superpowers/plans/2026-05-11-send-permission-p0.md`
- Previous P1 plan: `docs/superpowers/plans/2026-05-11-send-status-p1.md`

## Scope

Implement now:

- `message.App.Send` returns success for `Framer.NoPersist=true` after sender/channel validation and P0/P1 permissions pass.
- NoPersist success returns `ReasonSuccess`, `MessageID=0`, and `MessageSeq=0`.
- NoPersist send does not call channel cluster append and does not submit committed-message events.
- NoPersist send does not require `ChannelCluster`, `MetaRefresher`, or `RemoteAppender` because durable append is skipped.
- NoPersist still respects existing permission denials such as `SendBan`, `Ban`, `Disband`, denylist, subscriber, and allowlist checks.
- `/message/send` accepts legacy-compatible `header.no_persist`; optionally accept top-level `no_persist` as a cheap alias.
- Update raw difference docs and project knowledge after behavior changes.

Do not implement now:

- `SyncOnce` cmd-channel conversion.
- Request-scoped `subscribers`.
- Temporary-channel or message-scoped subscriber-source delivery wiring.
- Real-time online delivery for non-durable messages.
- `/message/sendbatch`.
- Plugin hooks, `PersistAfter`, storage webhook, offline webhook, or AI hooks.
- New deployment branches that bypass the single-node-cluster model.

## Files

Modify:

- `internal/usecase/message/send_test.go` — add NoPersist behavior tests and update durable append test fixtures to stop using `NoPersist=true`.
- `internal/usecase/message/send.go` — add the post-permission NoPersist shortcut before cluster-required checks and `sendDurable`.
- `internal/access/api/integration_test.go` — add an HTTP compatibility test for `header.no_persist` with no configured cluster.
- `internal/access/api/message_send.go` — parse `header.no_persist` and map it to `message.SendCommand.Framer.NoPersist`.
- `docs/raw/send-path-business-logic-diff.md` — mark P2a NoPersist non-durable boundary as restored and list remaining P2/P3 gaps.
- `docs/development/PROJECT_KNOWLEDGE.md` — add one concise note for NoPersist send semantics.

Do not modify:

- `pkg/channel/handler/append.go` — durable append remains business-rule free and should not branch on NoPersist.
- `internal/access/gateway/*` — gateway already carries `frame.Framer`; P2a does not change sendack mapping.
- Config files — P2a adds no config.

---

### Task 1: Restore NoPersist Usecase Shortcut

**Files:**

- Modify: `internal/usecase/message/send_test.go`
- Modify: `internal/usecase/message/send.go`

- [ ] **Step 1: Add failing usecase tests**

Add tests near the existing send validation/permission tests in `internal/usecase/message/send_test.go`:

```go
func TestSendNoPersistReturnsSuccessWithoutCluster(t *testing.T) {
	app := New(Options{Now: fixedNowFn})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true},
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Zero(t, result.MessageID)
	require.Zero(t, result.MessageSeq)
}

func TestSendNoPersistSkipsDurableAppendAndCommittedDispatch(t *testing.T) {
	cluster := &fakeChannelCluster{}
	dispatcher := &recordingCommittedDispatcher{}
	app := New(Options{
		Now:                 fixedNowFn,
		Cluster:             cluster,
		CommittedDispatcher: dispatcher,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true},
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Zero(t, result.MessageID)
	require.Zero(t, result.MessageSeq)
	require.Empty(t, cluster.sendRequests)
	require.Empty(t, dispatcher.calls)
}

func TestSendNoPersistStillChecksPermissions(t *testing.T) {
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("u1", int64(frame.ChannelTypePerson))] = metadb.Channel{
		ChannelID:   "u1",
		ChannelType: int64(frame.ChannelTypePerson),
		SendBan:     1,
	}
	app := New(Options{
		Now:             fixedNowFn,
		PermissionStore: permissions,
	})

	result, err := app.Send(context.Background(), SendCommand{
		Framer:      frame.Framer{NoPersist: true},
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSendBan, result.Reason)
}
```

Update `TestSendReturnsSuccessAfterDurableWriteAndSubmitsCommittedMessage` so durable-path fixtures do not set `NoPersist=true`. Keep other flags, for example:

```go
Framer: frame.Framer{RedDot: true, SyncOnce: true},
```

- [ ] **Step 2: Run RED usecase tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/message -run 'TestSendNoPersist|TestSendReturnsSuccessAfterDurableWriteAndSubmitsCommittedMessage' -count=1
```

Expected: FAIL because NoPersist still reaches the cluster-required/durable path.

- [ ] **Step 3: Implement minimal usecase shortcut**

In `internal/usecase/message/send.go`, insert the shortcut immediately after permission success and before `a.cluster == nil`:

```go
	if cmd.Framer.NoPersist {
		return SendResult{Reason: frame.ReasonSuccess}, nil
	}
```

Do not add any cluster/meta/delivery logic in this branch.

- [ ] **Step 4: Run usecase tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/message -run 'TestSendNoPersist|TestSendReturnsSuccessAfterDurableWriteAndSubmitsCommittedMessage' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit usecase change**

```bash
git add internal/usecase/message/send.go internal/usecase/message/send_test.go
git commit -m "feat: skip durable append for nopersist sends"
```

---

### Task 2: Add HTTP `header.no_persist` Compatibility

**Files:**

- Modify: `internal/access/api/integration_test.go`
- Modify: `internal/access/api/message_send.go`

- [ ] **Step 1: Add failing API integration test**

Add a second `/message/send` integration test in `internal/access/api/integration_test.go`:

```go
func TestAPIServerSendMessageNoPersistHeaderSkipsClusterRequirement(t *testing.T) {
	msgApp := message.New(message.Options{})
	srv := New(Options{
		ListenAddr: "127.0.0.1:0",
		Messages:   msgApp,
	})
	require.NoError(t, srv.Start())
	t.Cleanup(func() {
		require.NoError(t, srv.Stop(context.Background()))
	})

	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + srv.Addr() + "/healthz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 2*time.Second, 20*time.Millisecond)

	body := map[string]any{
		"from_uid":     "u1",
		"channel_id":   "u2",
		"channel_type": float64(frame.ChannelTypePerson),
		"payload":      base64.StdEncoding.EncodeToString([]byte("hi")),
		"header": map[string]any{
			"no_persist": float64(1),
		},
	}
	payload, err := json.Marshal(body)
	require.NoError(t, err)

	resp, err := http.Post("http://"+srv.Addr()+"/message/send", "application/json", bytes.NewReader(payload))
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		MessageID  int64  `json:"message_id"`
		MessageSeq uint64 `json:"message_seq"`
		Reason     uint8  `json:"reason"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Zero(t, got.MessageID)
	require.Zero(t, got.MessageSeq)
	require.Equal(t, uint8(frame.ReasonSuccess), got.Reason)
}
```

This test intentionally configures no cluster. Without `header.no_persist` mapping, the request should fail through `ErrClusterRequired`.

- [ ] **Step 2: Run RED API test**

Run:

```bash
GOWORK=off go test ./internal/access/api -run TestAPIServerSendMessageNoPersistHeaderSkipsClusterRequirement -count=1
```

Expected: FAIL because the handler does not parse `header.no_persist` yet.

- [ ] **Step 3: Parse NoPersist request header**

In `internal/access/api/message_send.go`, add a narrow header struct:

```go
type sendMessageHeaderRequest struct {
	// NoPersist marks the send as non-durable when non-zero.
	NoPersist int `json:"no_persist"`
}
```

Extend `sendMessageRequest`:

```go
	Header    sendMessageHeaderRequest `json:"header"`
	NoPersist int                      `json:"no_persist"`
```

Map the flag into `SendCommand`:

```go
	noPersist := req.Header.NoPersist != 0 || req.NoPersist != 0

	result, err := s.messages.Send(reqCtx, message.SendCommand{
		TraceID: traceCtx.TraceID,
		Framer:  frame.Framer{NoPersist: noPersist},
		// existing fields unchanged
	})
```

Keep all existing request validation and response shape unchanged.

- [ ] **Step 4: Run API tests**

Run:

```bash
GOWORK=off go test ./internal/access/api -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit API change**

```bash
git add internal/access/api/message_send.go internal/access/api/integration_test.go
git commit -m "feat: accept nopersist send header"
```

---

### Task 3: Update Documentation

**Files:**

- Modify: `docs/raw/send-path-business-logic-diff.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Update raw difference analysis**

In `docs/raw/send-path-business-logic-diff.md`, update the NoPersist section to add a dated P2a status, for example:

```markdown
P2a 状态（2026-05-11）：当前项目已在 `internal/usecase/message` 的 P0/P1 权限检查之后恢复 `NoPersist` 非持久化边界。`Framer.NoPersist=true` 且权限通过时，发送返回 `ReasonSuccess`，`MessageID=0`，`MessageSeq=0`，不要求 channel cluster，不写入 durable channel log，也不提交 committed-message event。`/message/send` 已支持 `header.no_persist` 映射。

仍未恢复：`SyncOnce` cmd channel 转换、request-scoped subscribers、临时频道投递、sendbatch、plugin/webhook/AI 钩子，以及非持久化消息的完整在线临时投递链路。
```

Also update the P2 priority / P1-after-status summary so the remaining gap list no longer says the NoPersist non-durable boundary is entirely missing.

- [ ] **Step 2: Update project knowledge**

In `docs/development/PROJECT_KNOWLEDGE.md`, add one concise bullet under `Channel Runtime`, for example:

```markdown
- `NoPersist` sends still pass validation and send permissions, then skip durable append/committed events and return success with zero message ID/seq.
```

Keep the document short; do not repeat the full design.

- [ ] **Step 3: Commit documentation**

```bash
git add docs/raw/send-path-business-logic-diff.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs: update nopersist send semantics"
```

---

### Task 4: Final Verification

**Files:**

- Verify only; no expected file edits.

- [ ] **Step 1: Run focused tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/message ./internal/access/api -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader relevant tests**

Run:

```bash
GOWORK=off go test ./internal/... ./pkg/slot/... -count=1
```

Expected: PASS.

- [ ] **Step 3: Inspect git state**

Run:

```bash
git status --short
git log --oneline --decorate -5
```

Expected: clean worktree, branch `feature/send-nopersist-p2a` contains the design, plan, implementation, and documentation commits.

- [ ] **Step 4: Prepare completion report**

Report:

- NoPersist usecase behavior restored after permissions.
- HTTP `header.no_persist` compatibility added.
- Docs updated with remaining non-goals.
- Exact verification commands and pass/fail status.

Then use `superpowers:finishing-a-development-branch` to decide whether to merge the feature branch to `main`, create a PR, or leave it for review.
