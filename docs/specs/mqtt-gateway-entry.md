# MQTT gateway lifecycle entry

This slice connects the existing PacketHandler contract to Session acquisition,
the connection supervisor and authenticated Publisher. It is an internal
integration surface; product MQTT admission remains disabled until subscription,
delivery, source protection, recovery and restore composition are complete.

## Failure inventory before implementation

1. Unverified credentials or malformed Will evict an existing owner. Map owned
   input first and let the Session usecase authenticate before any acquisition.
2. CONNECT defaults truncate client limits, rejection ignores Maximum Packet Size,
   or arbitrary dependency text leaks. Preserve uint32 limits on both outcomes,
   emit fixed valid CONNACK reasons and advertise only explicit bounded capabilities.
3. The owner retires between acquisition and CONNACK. Register before acceptance
   and retain an admitted operation until gateway open or rollback. Its context
   belongs to the gateway request, not the temporary authentication timeout.
   Generic gateway CheckReply revalidates before enqueue; rejection/panic rolls back.
4. A cancelled acquisition, failed registration, panic or failed CONNACK leaks a
   candidate. Before registration, perform bounded lifecycle cleanup; afterwards,
   release the handshake scope and enqueue exactly one abnormal disconnect.
5. A close callback joins the PUBLISH operation that called it. Close/rollback
   callbacks only release handshake state and enqueue nonblocking cleanup. All
   joined work belongs to the existing supervisor, without per-client workers.
6. Session values supply a forged UID or a state from another gateway connection.
   Keep private immutable connection evidence bound to its gateway Session ID;
   Publisher obtains its principal from Owners. Control packets admit the same owner.
7. Normal disconnect is overwritten by transport loss, or delayed cleanup moves
   its clock. Capture trusted monotonic observation and preserve the first intent.
   Validate client reason direction and expiry before accepting normal intent;
   CONNECT expiry zero cannot become nonzero through DISCONNECT.
8. Fenced/closed sessions still ping, publish or cancel a Will. Check admission
   and explicit closing state; close on write failure and never reopen state.
9. Unsupported subscription/delivery controls silently succeed. Close with a
   fixed implementation-specific reason until those paths are implemented.
10. A fake writer hides TCP handshake/cleanup failures. Verify a real Paho client
    over the real gnet gateway and single-node cluster with 256 hash Slots:
    authentication, persistence before ACK, wrong-token non-eviction, same-ID
    takeover, resume, normal/abnormal Will decision, and failed-CONNACK cleanup.

Protocol direction and disconnect semantics follow OASIS MQTT 5.0 sections
[3.14.2 and 3.14.4](https://docs.oasis-open.org/mqtt/mqtt/v5.0/os/mqtt-v5.0-os.html).
Client timestamps and reason strings never enter cleanup authority or diagnostics.

## Integration findings

Real Paho DISCONNECT closes TCP immediately after writing. Gateway may observe
EOF before its ordered mailbox dispatches the decoded packet. A failing Paho
Will-cancellation scenario and deterministic callback-order regression demonstrated
the loss. The MQTT codec adapter now keeps one constant-size first-disconnect
receipt (reason, optional expiry, server-reference presence and monotonic receipt
time). The close callback validates that receipt against accepted Session policy;
it retains no client text or payload. Physical closure still cancels publication
work immediately; this receipt does not execute queued business messages.

A second regression ensures normal intent reaches Connections before an explicit
owner fence. Reversing that order allowed concurrent renewal to synthesize an
abnormal intent first. Both failures were reproduced before their fixes.

The entry currently rejects subscription and unsubscription control packets.
Subsequent outbound binding supports durable PUBACK; the
[connection handoff](mqtt-connection-delivery.md) registers scheduled delivery
after open and wakes it after ACK or close. CONNACK disables subscription identifiers until that capability
is composed. No database columns, commands, listener config or product capability
gate are added here. This internal integration is not full product acceptance.

## Frozen context

Source `ffdf821a9`; SHA-256 digests:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/access/mqtt/FLOW.md`: `814ebcfb77565d1a30f178d205eb5ac446f08db9b057959770ec331cce566266`
- `internal/app/FLOW.md`: `82d8fae08a58a287cd72ecad51479b584a7d498af63891722e4c7013618fc374`
- `internal/runtime/mqttsession/FLOW.md`: `cd6b01821860621d502db203c9dbd29604e9bab459d78c584022ce43802c6320`
- `internal/usecase/mqttsession/FLOW.md`: `b58f90f775c8a49eadc7dfb0ce562cd0cffd958b129b6df5be81a5787970b70d`
- `pkg/gateway/FLOW.md`: `3e2270d966269b2835a32c54e001467aba014cc5554e4db4f253e11878348dc7`
