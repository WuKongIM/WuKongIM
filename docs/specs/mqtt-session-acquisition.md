# MQTT Session acquisition and renewal

This usecase connects authenticated CONNECT intent, exact-owner isolation,
committed Session/Will transitions and the owner-local execution gate. The full
approved MQTT feature still requires product composition, subscription/replay,
permission revocation, recovery and process-level acceptance.

## Failure inventory before implementation

1. Invalid credentials, ClientID/UID rebinding, malformed Will content or denied
   Will authorization isolates an existing connection or writes Session metadata.
   Validate and authenticate before those effects, then recheck authorization
   after the potentially slow isolation call. Never use WK master-device kicks.
2. A pending candidate executes before its exact lifecycle commit; conflicts,
   malformed results, uncertain commits or expired local reservations activate
   it anyway. Reserve before commit, activate only on a complete committed result,
   and permanently fence/close a failed local candidate with bounded cleanup.
3. A missing local owner, offline/ended metadata, wall-clock expiry, foreign boot
   or failed RPC is mistaken for exact-owner isolation. The injected isolation
   port must prove every prior owner; this implementation uses explicit quiescence.
   Unreachable-owner/restore proofs remain required work, never a time fallback.
4. Owner changes while isolation is in flight. Reread authoritative metadata and
   require the same complete old owner; a changed owner fails without attempting
   to evict an unobserved successor or spinning in an unbounded retry loop.
5. Renewed delivery counters/revisions during old-owner drain are overwritten by
   the initial read. Build the proposal from the post-isolation authoritative row.
   Resume preserves backlog/window allocators; Clean Start/new lifetime resets
   them. Lower Receive Maximum does not delete an existing larger window.
6. An expired active owner reconnects without recording its disconnect, silently
   canceling a due Will or restarting the offline lifetime. After isolation, first
   commit abnormal disconnect at its recorded execution deadline, then acquire.
   Reject inconsistent clocks rather than move timestamps backward or guess proof.
7. Network/commit latency extends a local lease. Capture the local monotonic
   deadline before proposing, retain that exact deadline through acknowledgement,
   and let local activation/renewal reject late receipts. Round the durable
   millisecond upper bound upward so it cannot expire before the local gate.
   Never reconstruct a
   local deadline from a remote absolute wall-clock value.
8. A renewal resurrects an expired/replaced owner, overwrites a Will or delivery
   counters, or changes the old gate after an ambiguous write. Renew inside an
   admitted scope, compare exact ownership/revision and preserve all other fields.
   Unconfirmed renewal leaves the previous deadline; confirmed owner loss, invalid
   evidence or clock regression fences immediately. Failed local installation
   permanently fences that owner without waiting on its own operation scope.
9. Stale disconnect or a client expiry override corrupts a new owner or extends
   an original zero-expiry session. Quiesce outside admitted scopes, reread exact
   identity and commit the atomic lifecycle decision; cap negotiated expiry at
   24 hours. Invalid overrides fail before closing a still-live owner. Capture disconnect
   observation before blocking isolation so its delay cannot turn an accepted
   normal disconnect into a Will publication or restart the offline clock.
10. Will templates forge publisher identity/server idempotency or retain mutable
    caller byte slices. Validate the entry-neutral publication contract, clone
    bounded content, and derive Will identity from the committed Session revision.
    Current publish authorization is separately required when the Will executes.
11. A canceled admission failure leaves a candidate open or cleanup blocks the
    caller indefinitely. A nonblocking local Fence closes admission immediately;
    detached cleanup is bounded, and errors retain runtime capacity for a sweep.
12. A dependency panic leaks the renewal scope. Release the scope on every exit,
    including panic, before any local fencing can join it.
13. Per-client locks, queues, retries or workers grow without bound. The usecase
    owns no registry/worker: local runtime bounds reservations/scopes; one request
    performs bounded reads/proposals, and concurrent acquisition is settled by CAS.

## Authority and clock contract

The metadata port is the existing foreground-gated cluster Node facade. Reads
must use current Slot authority and writes return committed conditional results.
Using its narrow shared metadata contracts does not permit local storage bypass
in product composition. Test storage adapters are not product authority evidence.

The isolation port returns nil only after exact-owner execution and transport
isolation. This service never derives that proof from a Session row or time.
The current owner RPC supplies a real quiescence proof for a reachable matching
registry. Unreachable/restarted owner recovery needs an additional valid proof
implementation before the full product can be enabled.

The injected clock shares the local Owners monotonic time base, and the configured
lease duration must fit that registry's MaxLease. Each new/renewed
execution grant captures its deadline before the durable proposal; the receipt
cannot move it. The durable millisecond lease upper bound rounds upward by less
than one millisecond; the local monotonic deadline remains unchanged. Absolute
milliseconds record Session/Will lifecycle time, but
are never remote isolation evidence. Clock regression against the current row,
invalid monotonic time, timestamp overflow and delayed lease installation fail.
Default expiry policy is capped at 86,400 seconds; lifetime quotas are fixed at
creation and preserved on resume. A concurrent CAS conflict asks the client or
worker to retry a new request; it does not allocate another local candidate.

Disconnect callers must first release their own admitted operation scope. A
nonblocking local Fence may be used by close callbacks before scheduling durable
cleanup. The usecase itself creates no background task or cleanup goroutine.

## Frozen context

Source `571963d33`; SHA-256 digests follow.

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`

- `internal/runtime/mqttsession/FLOW.md`: `49020c1513f6d4eebe9dba40d9aea0077979d3221ed083e7a4bf7b3b015e260c`

- `internal/contracts/mqttsession/FLOW.md`: `5bd4d98da6fa4d59615b62f31f98056add1127274a29816c8d5a181015329841`

- `internal/usecase/presence/FLOW.md`: `aab89d6a9ebf83940b575c99522bd169413276810c59f2214cdd288c8560a1b2`

- `internal/usecase/user/FLOW.md`: `74c5249e1f0c6424f5ba63a059ba28d2d08d7fa3af3ec75a43fa5c978e22455c`

- `internal/access/mqtt/FLOW.md`: `9dcd77d3a6be9170f40b4de7959a541942c6844848e226174a8f7e1b733300cd`

- `pkg/db/meta/FLOW.md`: `e05d11e22fc0c4afa0898a9e6498b45b9cc7f594fd4b8529587056cd784ecbb7`

- `pkg/cluster/FLOW.md`: `6ac161946f38f7f4a2f3f870da68d62b3f8008d5653e45dd26707f5b77415bc8`

- `internal/app/FLOW.md`: `8dab6a38e7e158056e187b4ae2e56b60d3bceb8728e955e870820c3976ed209c`

- `internal/usecase/message/FLOW.md`: `161490413a1676815da7834ca2216857fc0cc4c5e2e00fe1a965abc7fcf22357`

- `pkg/protocol/publication/FLOW.md`: `c233c21b41ca6cfc2f4c34ffec50bba4eb099952591856b4f3233f231a2ec655`
