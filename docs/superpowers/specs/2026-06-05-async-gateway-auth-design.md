# Async Gateway Auth Design

## Context

`pkg/gateway/core.Server` already moves authenticated business frames off the transport callback path through a bounded async dispatch queue. The remaining synchronous hotspot is CONNECT handling: `handleAuthFrame` runs `Authenticator.Authenticate`, optional `SessionActivator.OnSessionActivate`, CONNACK write, and `OnSessionOpen` before `onData` can return.

In the current architecture, activation can call presence routing and cluster-facing logic. A slow token check or activation route therefore blocks the connection actor callback path and can delay later transport events for the same connection. Authentication should use the same bounded-worker principle as SEND dispatch while preserving the CONNECT protocol contract.

## Goals

- Move the business part of CONNECT authentication and activation off the transport event path.
- Keep authentication concurrency bounded and observable.
- Preserve current success and failure behavior: CONNACK reason selection, activation rollback, auth failure classification, and `OnSessionOpen` ordering.
- Reject protocol traffic that arrives before authentication finishes. The gateway must not buffer post-CONNECT business frames while auth is pending.
- Keep the change local to `pkg/gateway` core and public gateway types unless configuration wiring is explicitly needed later.

## Non-Goals

- Do not change authentication semantics in `NewWKProtoAuthenticator`.
- Do not introduce a bypass path for cluster or presence activation.
- Do not batch authentication requests.
- Do not reuse the SEND micro-batch path for CONNECT.
- Do not add a large new service layer or app-level orchestration object.

## Chosen Approach

Add a dedicated bounded async auth queue owned by `core.Server`.

`handleAuthFrame` remains responsible for protocol gating only. When a session requires auth and has not authenticated:

1. Non-CONNECT frame: close with `policy_violation`.
2. CONNECT while auth is already pending: close with `policy_violation`.
3. First CONNECT: mark auth pending, clone the CONNECT packet, enqueue an auth task, and return immediately.
4. Auth queue full: best-effort write a retryable CONNACK with `ReasonSystemError`, then close with an auth-specific queue-full close reason.

Auth workers run the existing authentication body asynchronously:

```text
Authenticate
  -> write session values
  -> optional OnSessionActivate
  -> normalize/write CONNACK
  -> rollback activation if success CONNACK write fails
  -> set authenticated
  -> dispatch OnSessionOpen
```

The queue is separate from `asyncDispatchQueue` so CONNECT cannot be delayed behind high-volume SEND traffic, and SEND-specific sharding and batching stay focused on authenticated frames.

## State Model

Add `authPending` to `sessionState`, guarded by the existing `metaMu`.

```text
requiresAuth=true, authenticated=false, authPending=false
  first CONNECT accepted -> authPending=true
  non-CONNECT or duplicate CONNECT -> close(policy_violation)

auth worker success:
  authPending=false
  authenticated=true
  OnSessionOpen dispatched after success CONNACK

auth worker failure or close:
  authPending=false
  session closes
```

Workers check `state.isClosed()` before expensive work, before writing CONNACK, and before dispatching `OnSessionOpen`. If the peer closes while auth is queued or running, the worker exits without resurrecting the session.

## Queue And Workers

Introduce an `asyncAuthQueue` parallel to the existing async dispatch queue:

- Sharded bounded channels, or a single bounded channel if the implementation keeps the worker count modest.
- `Start()` creates the queue after listeners start, alongside idle monitor and async frame dispatch.
- `Stop()` swaps the queue to nil, closes it, closes sessions, and waits through the existing `workerWG`.
- Submitting is non-blocking. Queue-full never falls back to synchronous authentication.

Default worker sizing should be conservative and code-local. A good first implementation is:

- `workers = min(max(runtime.GOMAXPROCS(0)*4, 16), 64)`.
- `capacityPerWorker = 128`, capped by a total auth queue capacity such as `8192`.

These values keep the transport path protected without adding new user-facing config in the first pass. If production evidence shows a need for tuning, add explicit config later with comments and `wukongim.conf.example` alignment.

## Cloning And Ownership

The auth task must own its CONNECT data. Add a narrow `cloneAuthConnectPacket` helper that shallow-copies scalar fields and deep-copies any byte slice fields if `ConnectPacket` gains them later. This avoids asynchronous reads from protocol adapter-owned frame objects.

The task stores:

- `state`
- `replyToken`
- cloned `*frame.ConnectPacket`
- `enqueuedAt`
- queue pointer for depth accounting

## Error And Close Semantics

Existing auth failure classes stay unchanged:

- `protocol_violation`
- `authenticator_error`
- `activation_error`
- activation route classifier values
- CONNACK reason classifier values
- `connack_write_error`

Add auth-specific queue overflow types:

- `ErrAsyncAuthQueueFull`
- `CloseReasonAsyncAuthQueueFull`
- auth failure class `auth_queue_full`

On auth queue overflow, the gateway should attempt to write `ReasonSystemError` CONNACK with the request reply token, record `Observer.OnAuth` as failed with `auth_queue_full`, then close. If the CONNACK write fails, close using the write error mapping.

## Observability

Keep the existing `Observer.OnAuth` contract. For async auth, `AuthEvent.Duration` should measure total time from enqueue to final auth outcome, including queue wait. This keeps metrics compatible and reflects client-observed handshake latency.

Do not extend `AsyncSendObserver`; it is SEND-specific by name and should not become a generic queue observer. A later metrics pass can add an `AsyncAuthObserver` or gateway auth queue metrics if operational evidence calls for split queue wait and execution time.

## Documentation Updates

Update `pkg/gateway/FLOW.md` after implementation:

- `core.Server` manages both authenticated frame async dispatch and async CONNECT auth.
- `OnData` still decodes synchronously, but CONNECT business authentication is enqueued.
- During `authPending`, any additional inbound frame is a protocol violation and closes the session.
- CONNECT success and failure ordering remains unchanged from the client's perspective.

No package-local `pkg/gateway/core/FLOW.md` exists today, so the package-level FLOW document is the authoritative flow doc for this change.

## Tests

Add or update focused unit tests under `pkg/gateway/core`:

- CONNECT auth does not block `EmitData` while `Authenticator.Authenticate` is blocked.
- Auth pending plus a later frame closes with `policy_violation` and does not call handler frame paths.
- Duplicate CONNECT during pending auth closes with `policy_violation`.
- Auth queue full records failed auth, writes `ReasonSystemError` when possible, closes with `CloseReasonAsyncAuthQueueFull`, and does not run auth synchronously.
- Successful auth still writes CONNACK before `OnSessionOpen`.
- Activation failure classification and rollback behavior remain unchanged.
- Peer close while auth is queued or running does not write CONNACK or dispatch `OnSessionOpen`.
- `Stop()` drains workers without leaking goroutines.

Run at least:

```bash
go test ./pkg/gateway/...
```

If implementation touches config or app wiring later, also run the affected `cmd/wukongim`, `cmd/wukongimv2`, and `internal/app` tests.

## Risks

- Existing tests that assume synchronous auth completion need to wait for CONNACK or observer events.
- Closing on any pending-auth follow-up frame is stricter than buffering, but it matches the requested protocol contract and avoids unbounded per-session pending work.
- `AuthEvent.Duration` will increase under queue pressure because it includes wait time. This is intentional for client-visible latency, but dashboards should be read with that in mind.

