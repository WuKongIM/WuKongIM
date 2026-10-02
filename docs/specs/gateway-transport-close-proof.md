# Gateway physical close proof

This is the transport seam required by MQTT exact-owner quiescence. It does not
acquire Session authority, drain business scopes or establish a distributed lease.

## Failure inventory before implementation

1. Logical Session.Close, a successful enqueue, or gnet OnClose returns before
   the socket closes. In gnet v2.9.7, OnClose precedes residual writes and the
   close syscall. Only CloseWithCallback completion can supply this boundary.
2. A canceled wait erases the operation, a retry queues more closes, or a late
   callback changes a failed submission into success. One lazily allocated close
   receipt per connection joins submission and callback completion; errors stick.
3. A synchronous callback runs before submission returns and publishes premature
   success. Receipt completion must account for both halves, including an
   ambiguous non-nil submission error after a callback.
4. Ordinary writes remain admitted after isolation starts. Fence Session writes
   without waiting for a stalled encoder, then fence transport writes. An already
   entered effect remains the owner runtime's scope-drain responsibility.
5. Physical close waits for OnSessionClose, the actor queue, an in-progress open
   callback or itself. The narrow operation must never invoke/wait for business
   cleanup; ordinary transport-close dispatch owns those callbacks separately.
6. Unsupported transports or Session implementations silently fall back to
   logical close. The optional capability must fail explicitly without proof.
7. TCP and WebSocket differ: a WS close frame enqueue is not socket completion.
   Isolation requests the raw socket close with completion; it does not require a
   graceful WebSocket handshake or claim that pending application bytes arrived.
8. Concurrent retries allocate unbounded workers/timers/queued close requests.
   Use one retained receipt, no background waiter or per-connection worker.

## Contract

`transport.CloseWaiter.CloseAndWait(ctx)` fences future writes and returns nil
only after the physical close callback succeeds. Cancellation bounds the caller's
wait, not the underlying close operation. Subsequent calls join the same receipt.
A non-nil context that is already canceled still requests close, then stops the
wait; this matters when core fences/cancels its own request context first. Core
rejects an already canceled caller before starting its admission fence.
Submission/callback errors remain failure even if a later callback reports nil.
The capability is optional; ordinary Conn.Close keeps its existing semantics.

`session.OutboundFencer.FenceOutbound()` permanently closes ordinary write
admission without waiting for already entered encoders. The ordinary Close method
continues to join serialized writes as before.

`types.Context.CloseTransportAndWait(ctx, reason)` requires both capabilities,
fences inbound/outbound admission, cancels request work, and requests physical
close. It does not call or wait for protocol/business cleanup. Core's normal close
notification remains ordered after open; owner quiescence separately joins every
admitted business scope. A missing capability, context cancellation or error is
not proof. Neither successful close nor scope drain proves client receipt.

## Frozen context

Source: `0ddb24890`. Applicable `AGENTS.md` and `pkg/gateway/FLOW.md` are frozen
below. Dependency behavior was checked in the pinned gnet v2.9.7 source:
`eventloop_unix.go` close (OnClose before syscall) and `connection_unix.go`
CloseWithCallback (callback after the event-loop close operation).

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`

- `pkg/gateway/FLOW.md`: `413d94fcf7d3d11468bdc24625918dc258a882997cdc280c201cd8ce843f77f0`
