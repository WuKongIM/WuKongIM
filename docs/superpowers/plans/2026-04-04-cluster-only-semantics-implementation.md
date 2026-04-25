# Cluster-Only Semantics Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the remaining true single-machine message-send semantics so one-process deployment is treated as a `单节点集群`, not a local-only mode.

**Architecture:** Tighten `internal/usecase/message` so it accepts an explicit `ChannelCluster` dependency, returns a deterministic misconfiguration error when the cluster is missing, and always uses the durable `channellog` write path for person-message send. Then update send-oriented access tests to inject a cluster-backed message app explicitly instead of relying on the deleted local fallback. Finally sweep the project rules and historical design docs so deployment-mode wording consistently uses `单节点集群` or `single-node cluster` rather than implying a separate single-machine mode.

**Tech Stack:** Go 1.23, `internal/usecase/message`, `internal/access/api`, `internal/access/gateway`, `internal/app`, `pkg/storage/channellog`, `testing`, `testify`, `rg`.

**Spec:** `docs/superpowers/specs/2026-04-04-cluster-only-semantics-design.md`

---

## File Map

| Path | Responsibility |
|------|----------------|
| `internal/usecase/message/app.go` | constructor options, injected collaborators, message-layer errors |
| `internal/usecase/message/send.go` | send orchestration and durable-send contract |
| `internal/usecase/message/send_test.go` | direct behavior coverage for cluster-only message send |
| `internal/app/build.go` | composition root wiring for the message use case |
| `internal/access/gateway/handler_test.go` | gateway adapter coverage with injected real message app |
| `internal/access/gateway/integration_test.go` | end-to-end gateway send flow under a cluster-backed message app |
| `internal/access/api/integration_test.go` | HTTP send endpoint coverage with a cluster-backed real message app |
| `AGENTS.md` | repository-wide architecture rules |
| `docs/superpowers/specs/2026-04-01-app-bootstrap-design.md` | old bootstrap design wording that still mentions `single-node mode` |
| `docs/superpowers/plans/2026-03-26-wkdb-slot-shard-snapshot-implementation.md` | historical implementation wording that still says `single-node bootstrap` |
| `docs/superpowers/plans/2026-03-28-wkcluster-implementation.md` | historical plan text that still frames deterministic tests as `single-node` work |
| `docs/superpowers/plans/2026-03-29-wkcluster-storage-separation.md` | historical plan wording that still describes test setup as `single-node` |
| `docs/superpowers/plans/2026-04-01-app-bootstrap-implementation.md` | historical bootstrap plan wording that still says `single-node config` and `single-node integration test` |

## Task 1: Enforce cluster-only message semantics

**Files:**
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `internal/app/build.go`

- [ ] **Step 1: Write the failing message tests**

Update `internal/usecase/message/send_test.go` so the desired contract is explicit before touching production code:

- replace the current local-fallback success test with a durable-send test that injects a fake `ChannelCluster` and asserts the returned `MessageID` and `MessageSeq` come from the fake durable result
- replace the current `ReasonUserNotOnNode` test with a durable-send success test that has no local recipient and still expects `ReasonSuccess`
- replace the current local delivery failure test with a durable-send test that commits successfully and still returns `ReasonSuccess` even when `Delivery.Deliver` fails
- add a new missing-cluster test that expects a deterministic message-layer error when `Send(...)` is called without a configured cluster
- update `TestNewPreservesInjectedCollaborators` to use a typed cluster dependency and to stop asserting the removed sequence allocator

Test sketch:

```go
func TestSendReturnsClusterRequiredWhenClusterNotConfigured(t *testing.T) {
	app := New(Options{Now: fixedNowFn})

	result, err := app.Send(context.Background(), SendCommand{
		SenderUID:   "u1",
		ChannelID:   "u2",
		ChannelType: wkframe.ChannelTypePerson,
		Payload:     []byte("hi"),
	})

	require.ErrorIs(t, err, ErrClusterRequired)
	require.Equal(t, SendResult{}, result)
}
```

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/usecase/message ./internal/app -run "TestSend|TestNewPreservesInjectedCollaborators" -count=1`

Expected: FAIL because `message.Options` still uses `ClusterPort`/`Sequence`, `Send` still falls back to `sendLocalPerson`, and `internal/app/build.go` still wires a local sequence allocator into the message app.

- [ ] **Step 3: Write the minimal production changes**

In `internal/usecase/message/app.go`:

```go
var (
	ErrUnauthenticatedSender = errors.New("usecase/message: unauthenticated sender")
	ErrClusterRequired       = errors.New("usecase/message: channel cluster required")
)

type Options struct {
	IdentityStore IdentityStore
	ChannelStore  ChannelStore
	Cluster       ChannelCluster
	MetaRefresher MetaRefresher
	Online        online.Registry
	Delivery      online.Delivery
	Now           func() time.Time
}

type App struct {
	identities IdentityStore
	channels   ChannelStore
	cluster    ChannelCluster
	refresher  MetaRefresher
	online     online.Registry
	delivery   online.Delivery
	now        func() time.Time
}
```

In `internal/usecase/message/send.go`:

```go
func (a *App) Send(ctx context.Context, cmd SendCommand) (SendResult, error) {
	if cmd.SenderUID == "" {
		return SendResult{}, ErrUnauthenticatedSender
	}
	if cmd.ChannelType != wkframe.ChannelTypePerson {
		return SendResult{Reason: wkframe.ReasonNotSupportChannelType}, nil
	}
	if a.cluster == nil {
		return SendResult{}, ErrClusterRequired
	}
	return a.sendDurablePerson(ctx, cmd)
}
```

Then delete the local-only pieces from `send.go`:

- `sendLocalPerson`
- `localPersonTarget`
- `resolveLocalPersonTarget`

Keep `deliverLocalPerson` exactly as post-commit best-effort local fanout.

In `internal/app/build.go`:

- remove the `internal/runtime/sequence` import
- delete `sequenceAllocator := &sequence.MemoryAllocator{}`
- pass `Cluster: app.channelLog` into `message.New(...)`
- stop passing the removed `Sequence:` option

- [ ] **Step 4: Run the focused package tests to verify they pass**

Run: `go test ./internal/usecase/message ./internal/app -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/message/app.go internal/usecase/message/send.go internal/usecase/message/send_test.go internal/app/build.go
git commit -m "refactor(message): enforce cluster-only send semantics"
```

## Task 2: Update send-oriented access tests to inject cluster-backed message apps

**Files:**
- Modify: `internal/access/gateway/handler_test.go`
- Modify: `internal/access/gateway/integration_test.go`
- Modify: `internal/access/api/integration_test.go`

- [ ] **Step 1: Rewrite the access tests so they describe the new contract**

Change the send-oriented tests that currently rely on `message.New(message.Options{})` or `gateway.New(Options{})` local-send behavior:

- in `internal/access/gateway/handler_test.go`, update `TestNewSharesOnlineRegistryWithInjectedMessageApp` so the real message app is constructed with a fake `ChannelCluster`
- in `internal/access/gateway/integration_test.go`, rename `TestGatewayWKProtoHandlerRoutesLocalPersonSend` to a cluster-oriented name such as `TestGatewayWKProtoHandlerRoutesDurablePersonSend` and inject a real message app backed by a fake `ChannelCluster`
- in `internal/access/api/integration_test.go`, construct the real message app with a fake `ChannelCluster` instead of `message.New(message.Options{})`

Shared fake for these tests:

```go
type fakeChannelCluster struct {
	result channellog.SendResult
	err    error
}

func (f *fakeChannelCluster) ApplyMeta(channellog.ChannelMeta) error { return nil }

func (f *fakeChannelCluster) Send(context.Context, channellog.SendRequest) (channellog.SendResult, error) {
	return f.result, f.err
}
```

Use deterministic durable results, for example `MessageID: 88, MessageSeq: 9`, so gateway ack assertions and recipient recv assertions can stay exact.

- [ ] **Step 2: Run the targeted access tests to verify they fail**

Run: `go test ./internal/access/gateway ./internal/access/api -run "TestGatewayWKProtoHandlerRoutes|TestNewSharesOnlineRegistryWithInjectedMessageApp|TestAPIServerSendMessageWithRealMessageApp" -count=1`

Expected: FAIL after Task 1 because those tests still instantiate send-capable message apps without a cluster-backed dependency.

- [ ] **Step 3: Apply the minimal test-only fixes**

Implementation notes:

- keep using the real `message.App` in these tests; do not replace it with `fakeMessageUsecase` where the test is intentionally exercising real use-case behavior
- keep the `OnlineRegistry` shared between the handler and the injected message app so recipient delivery assertions still exercise real local fanout
- do not change production access-layer code unless a compile fix is strictly required by the renamed tests
- if helper code becomes noisy, add a `_test.go` helper in the same package instead of introducing a new shared test utility package

- [ ] **Step 4: Run the targeted access tests to verify they pass**

Run: `go test ./internal/access/gateway ./internal/access/api -run "TestGatewayWKProtoHandlerRoutes|TestNewSharesOnlineRegistryWithInjectedMessageApp|TestAPIServerSendMessageWithRealMessageApp" -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/access/gateway/handler_test.go internal/access/gateway/integration_test.go internal/access/api/integration_test.go
git commit -m "test(access): inject cluster-backed message apps"
```

## Task 3: Standardize deployment terminology on `单节点集群`

**Files:**
- Modify: `AGENTS.md`
- Modify: `docs/superpowers/specs/2026-04-01-app-bootstrap-design.md`
- Modify: `docs/superpowers/plans/2026-03-26-wkdb-slot-shard-snapshot-implementation.md`
- Modify: `docs/superpowers/plans/2026-03-28-wkcluster-implementation.md`
- Modify: `docs/superpowers/plans/2026-03-29-wkcluster-storage-separation.md`
- Modify: `docs/superpowers/plans/2026-04-01-app-bootstrap-implementation.md`

- [ ] **Step 1: Capture the remaining wording that still implies a separate single-machine mode**

Run:

```bash
rg -n "single-node mode|single-node bootstrap|single-node config|single-node integration test|单机模式|单机部署" \
  AGENTS.md \
  docs/superpowers/specs/2026-04-01-app-bootstrap-design.md \
  docs/superpowers/plans/2026-03-26-wkdb-slot-shard-snapshot-implementation.md \
  docs/superpowers/plans/2026-03-28-wkcluster-implementation.md \
  docs/superpowers/plans/2026-03-29-wkcluster-storage-separation.md \
  docs/superpowers/plans/2026-04-01-app-bootstrap-implementation.md
```

Expected: matches in the files listed above.

- [ ] **Step 2: Rewrite the wording without expanding scope**

Editing rules:

- use `单节点集群` in Chinese sentences
- use `single-node cluster` in English sentences when a plain `single-node` phrase would otherwise read like a separate runtime mode
- remove or rewrite wording like `single-node mode`
- keep node-internal wording that is not about deployment mode
- do not rename internal library helper identifiers such as `startSingleNode` or `openSingleNodeLeader` in replication tests unless the surrounding sentence is explicitly documenting deployment semantics rather than test topology

Concrete examples:

```text
If later work adds single-node mode ...
=> If later work needs a single-node cluster bootstrap path ...

prefer a single-node bootstrap ...
=> prefer a single-node cluster bootstrap ...
```

- [ ] **Step 3: Re-run the wording check**

Run:

```bash
rg -n "single-node mode|single-node bootstrap|single-node config|single-node integration test|单机模式|单机部署" \
  AGENTS.md \
  docs/superpowers/specs/2026-04-01-app-bootstrap-design.md \
  docs/superpowers/plans/2026-03-26-wkdb-slot-shard-snapshot-implementation.md \
  docs/superpowers/plans/2026-03-28-wkcluster-implementation.md \
  docs/superpowers/plans/2026-03-29-wkcluster-storage-separation.md \
  docs/superpowers/plans/2026-04-01-app-bootstrap-implementation.md
```

Expected: no output

- [ ] **Step 4: Do a quick regression pass on the touched packages**

Run: `go test ./internal/usecase/message ./internal/access/gateway ./internal/access/api ./internal/app -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add AGENTS.md docs/superpowers/specs/2026-04-01-app-bootstrap-design.md docs/superpowers/plans/2026-03-26-wkdb-slot-shard-snapshot-implementation.md docs/superpowers/plans/2026-03-28-wkcluster-implementation.md docs/superpowers/plans/2026-03-29-wkcluster-storage-separation.md docs/superpowers/plans/2026-04-01-app-bootstrap-implementation.md
git commit -m "docs: standardize single-node cluster terminology"
```

## Final Verification

- [ ] Run: `go test ./internal/usecase/message ./internal/access/gateway ./internal/access/api ./internal/app -count=1`
- [ ] Expected: PASS
- [ ] Run: `git status --short`
- [ ] Expected: clean working tree except for intentionally untracked files unrelated to this plan

## Notes for the Implementer

- Follow `@superpowers:test-driven-development` task-by-task. The first visible change in each task should be a failing test or a failing grep-based wording check.
- Do not reintroduce any `a.cluster == nil` business fallback in `internal/usecase/message`.
- Treat `pkg/storage/channellog` as the only authority for durable `MessageID` and `MessageSeq`.
- Keep `deliverLocalPerson` as post-commit best-effort local fanout only.
- Production startup is already cluster-first in `internal/app`; the main implementation work is semantic cleanup plus test/doc realignment.
