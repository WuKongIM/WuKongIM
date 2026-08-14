# internal/usecase/benchterminal Flow

## Responsibility

`internal/usecase/benchterminal` owns the entry-agnostic, once-per-product-
process terminal cut for one exact benchmark `(run_id, assignment_id)`.
It does not parse HTTP or gateway frames, look up sessions, encode markers, or
write transports. Those operations remain in the entry adapter and gateway
session layer.

The controller is intentionally a one-shot generation capability. A new QPS
tier starts a new product process and owns a new controller; it is not a
general drain/restart control plane.

## Prepare Flow

```text
Prepare(exact run_id, assignment_id, expected_sessions)
  -> close gateway SEND admission and wait accepted mailbox work
  -> stop channelappend admission and wait accepted append/handoff work
  -> quiesce delivery-plan admission and wait owner push + pending RECVACK = 0
  -> issue non-zero uint64 epoch + opaque capability
  -> await exactly expected owner-local SessionFence admissions
```

The three ports run in that strict order against one detached context whose
total timeout is bounded by `Options.DrainTimeout` (default and maximum 90
seconds). The caller context only limits its own wait. A caller deadline does
not cancel previously accepted drain work; a later call with the exact same
identity joins the existing preparation. A port error, detached deadline, or
random-material failure makes the epoch permanently `failed` and issues no
grant. The detached pipeline is launched through the process goroutine
registry under one fixed low-cardinality App task; run and assignment identity
never enter supervisor labels.

`PrepareRequest` has the same public bounds as the target API: trimmed
`run_id` and `assignment_id` are at most 128 bytes, while
`expected_sessions` is at most 1,000,000. Product composition normally sets
`MaxSessions` to the smaller owner-local limit (2,500 for the lifecycle run).
The controller keeps identity internal: `Status` exposes only the closed
stage/failure enums, epoch, and bounded expected/sealed counts.

## Session Marker Flow

```text
authenticated terminal EVENT
  -> access adapter validates its current session ID == SessionFence.SessionID
  -> access adapter copies only epoch + SHA-256 capability proof
  -> Controller.SealAndEnqueue
     -> constant-time epoch and capability-digest comparison
     -> exact session-ID uniqueness and expected-count reservation
     -> per-call SessionSealer adapter
        -> current session write lock: seal ordinary outbound + enqueue ACK marker
  -> count local marker admission, never remote acknowledgement
```

`SessionSealer` is supplied to each `SealAndEnqueue` call rather than stored
in the controller. This prevents a usecase-to-entry/session registry cycle and
makes the access adapter responsible for binding the call to its authenticated
current session. Any seal error, duplicate session, stale/wrong proof, session
above the exact count, or adapter-reported post-seal ordinary frame permanently
fails the published epoch with the single low-cardinality
`protocol_violation` or `session_seal` status. The controller does **not** treat gnet
`AsyncWrite` callbacks, empty outbound buffers, TCP half-close, or marker
admission as proof that a client received the marker. Only the matching
client-observed marker acknowledgement closes receive proof.

The frame parser never exposes the raw capability. It derives a fixed-size
`Proof` digest and separately copies the nonce into the redacting
`SessionFence`; neither value is formatted or persisted by the access adapter.

## Secrets and Observability

`Grant.Capability` uses fixed-width 32-byte base64url material. Its `String`
and `GoString` methods redact it, and `Status` never contains capability,
run/assignment identity, nonce, or session IDs. Grant validation compares the
fixed-width capability and uint64 epoch in constant time. Raw adapter errors
and capability values must not become metrics, logs, status fields, or error
text.
