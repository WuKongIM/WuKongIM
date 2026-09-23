# MQTT owner execution and quiescence

This module supplies the owner-local half of the approved takeover contract.
It does not turn a socket-close request, a cancelled context or a deadline into
distributed proof that the old owner stopped. Runtime activation still needs a
committed Session owner and a conservative local execution deadline supplied by
the authoritative lease protocol. Product admission remains disabled until that
protocol, recovery and delivery are wired and verified.

## Failure inventory before implementation

1. Pending reservations execute before committed activation; a different UID,
   ClientID, lifetime, owner generation, process or connection affects the wrong
   owner. ClientID coexistence must not inherit WK master-device conflict rules.
2. A stale/repeated activation or renewal extends execution; an expired owner
   restarts because an expiry worker was late. Every operation checks its local
   deadline synchronously, independently of the sweep schedule.
3. Quiesce returns success after requesting socket close while an admitted
   operation can still act. Success requires the close callback to finish and
   every admitted operation to release its scope. Cancellation alone is no proof.
4. A close error, callback panic or caller cancellation releases capacity or
   reopens admission; concurrent close calls invoke competing callbacks. Failed
   close stays fenced and retained, and a later call can retry the exact owner.
5. A missing row is treated as closed despite delayed registration or process
   replacement. Connection IDs are allocated and published by this registry,
   never supplied by callers, reused or wrapped. Only retired issued IDs from
   this exact process can prove absence; foreign boots/future IDs fail closed.
6. Limits omit pending/closing owners or in-flight scopes; expiry queues grow
   with renewals; shutdown creates a goroutine per connection or scans all
   sessions on every tick. Hold one indexed deadline per retained owner, bound
   reservations/operations/sweep pages, and run callbacks outside locks.
7. Expiry and renewal race; a stale popped deadline closes a renewed owner;
   caller cancellation or duplicate scope release leaks counts. Snapshot counts
   contain no identities or credentials. Only the app owns repeated sweeps.
8. Gateway close adaption reports quiescence without sealing packet writes or
   closing the physical connection; runtime callbacks reenter quiescence and
   deadlock. The callback closes transport only, never waits for business cleanup.
9. Owner RPC accepts malformed/unbounded identities, targets a different node,
   interprets unsupported/missing/error responses as closure, or accepts a receipt
   for another owner. The closed versioned codec must echo the complete identity;
   cancellation and transient errors remain distinct from a quiesced receipt.

## Local contract

`Owner` includes broker namespace, ClientID, Session generation, owner generation,
node ID, boot ID and a registry-issued connection ID. It contains no credential,
packet or concrete socket. Each reservation retains a UID and a close callback
only on its node. The connection ID is independent of the gateway session ID.

Reserve is pending and bounded. Activation/renewal require an increasing durable
Session revision and a future deadline within the configured maximum local lease.
An exact receipt retry is idempotent but cannot move the deadline. The injected
clock and deadlines must share the same owner-local monotonic time base. They
are never serialized as cross-node fencing evidence. Clock correction and lease
grant derivation belong to the future authority adapter, not to wall-clock
comparisons inside this gate.

Begin admits one bounded operation before executing any business work, and its
scope must remain open through every associated effect and network write. Closing
the scope is explicit and idempotent; ignoring a cancelled scope keeps the owner
retained and prevents a false quiescence receipt. A scope must not escape into
untracked background effects. Metadata mutations still carry durable owner fences.

Quiesce first closes admission permanently and cancels operation contexts, then
closes the transport through the injected callback. It succeeds only when both
physical closure and scope drain are complete. Repeated requests coalesce; a
failed callback is retryable without removing the owner. A successful receipt is
specific to this exact owner, not a credential, future owner or publication grant.

Deadline expiry is an admission fence, not proof that already admitted work has
drained. The distributed takeover path must not substitute local expiry, presence
TTL, a boot mismatch or best-effort kick for the required isolation proof.

## Implemented owner RPC contract

Service 92 requests quiescence from the exact owner node through the existing
`CallRPC` transport. Request magic is `WKMQ`, response magic `WKMq`; both use
format byte 1 followed by an operation/status byte. Request operation 1 is
quiesce. Response statuses are 1 quiesced, 2 isolation unproved, 3 close failed,
4 cancelled and 5 deadline exceeded. Unknown formats/operations/statuses fail.

Both directions carry namespace, ClientID, Session generation, owner generation,
node ID, boot ID and connection ID in that order. Strings use big-endian uint16
byte lengths (1,024/1,024/128 maxima); integers are big-endian uint64. Frames are
bounded to 2,304 bytes and reject truncation/trailing bytes. The client accepts
success only for its complete exact requested identity; it neither reroutes nor
infers isolation from an unsupported service or communication failure.

The app has not registered this adapter or connected its runtime callback to
the gateway. Gateway `CloseSession` currently requests closure and does not
expose a physical completion proof. That boundary, foreground composition,
authoritative lease derivation and durable lifecycle acquisition remain required
before any product takeover or listener can use this module.

## Frozen context

Source `ec0367594`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/runtime/online/FLOW.md`: `c7233cdcec9976b5f237194ad7fd211c96e961702ce7621d7e759250a5dbf246`
- `internal/runtime/presence/FLOW.md`: `6c2543c49cb149194d9ca66e4244bf08b5a5b3e45210ebfdee477de33cd9bdee`
- `internal/usecase/presence/FLOW.md`: `aab89d6a9ebf83940b575c99522bd169413276810c59f2214cdd288c8560a1b2`
- `pkg/gateway/FLOW.md`: `413d94fcf7d3d11468bdc24625918dc258a882997cdc280c201cd8ce843f77f0`
- `internal/access/mqtt/FLOW.md`: `9dcd77d3a6be9170f40b4de7959a541942c6844848e226174a8f7e1b733300cd`
- `internal/access/node/FLOW.md`: `36aecd32738246d35ae8f9451e532d2dfd5a65c38b027e4d84eb8925df945fa8`
- `pkg/cluster/FLOW.md`: `c4c795c8db707d7a0ace32dc77dc0ac70550313ffb3e050ebb4fe898ce1d9c49`
