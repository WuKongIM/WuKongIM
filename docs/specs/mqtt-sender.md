# Bounded connection sender

Sender.Open binds one trusted activated Connection to one DeliverySink and
captures the existing DeliveryOrder ceiling. App/runtime creates exactly one
stream per connection before new window admission and owns scheduling/lifetime.
A nonblocking stream gate serializes one bounded Turn; there is no worker, body
queue, per-connection goroutine, or timer retransmission here.
Owners must allow at least two concurrent scopes for the turn plus preparation
or gateway admission, with additional bounded room for control work. Temporary
operation-capacity refusal yields Busy without binding an exchange or closing.

Each Turn first completes any already-enqueued downgraded-QoS-0 obligation, then
reads the next unsent durable exchange, and only when that prefix is exhausted
prepares the caller-selected source cursor. Captured old exchanges require
SessionPresent and use DUP; later admissions, including a recovered lost admission
reply on this connection, use DUP=false. The sent cursor advances only after
confirmed enqueue. Busy keeps no body and does not advance an exchange. Original
QoS 0 preclaims can be lost on Busy/expiry/failure, but are never retried.

Immediately before enqueue, read the current owner and exact exchange (or unchanged
QoS-0 subscription generation/revision), then perform fresh receive authorization
against the private permission incarnation captured by preparation. This last
authoritative authorization is the delivery ordering point. Revocation ordered
after it cannot retract an admitted write. The exact Owners scope covers this
check and gateway enqueue; it is released before any joined cleanup.

Definite denial/incarnation change fences the connection, closes it with an
application reason, and invokes exact-owner End. Failed ending remains explicit
pending work on the closed stream for bounded caller retry; a superseded owner
is discarded without following or reporting its successor ended. Unavailable
authority is not denial. Ambiguous/invalid enqueue results or callback panic fence and close
without same-connection resend. Close intent is not physical-isolation proof;
Owners/End retain that responsibility.

Successful downgraded QoS 0 keeps one private body-free completion token until
its original position and exact charge are authoritatively consumed. The sender
may reconcile this proved-enqueued token across unrelated parent revisions, but
may not re-evaluate QoS/expiry/No Local or rewrite the original debit. A lost reply
is resolved by the exact cursor position; no further message sends while pending.
The public WindowAdmission.CompleteQoS0 contract remains strict for other callers.

Handler.BindDelivery returns the accepted Connection and its bound DeliverySink.
It maps to the existing SendQoS1/SendQoS0 paths, preserving credit, exact PUBACK
bindings, property limits and physical-connection identity. Each turn replaces
the captured callback context with its bounded work context. Terminal feedback
uses MQTT DISCONNECT before closure (revocation 0x87, quota 0x97, other explicit
end/source-loss 0x83), valid codes from
[OASIS MQTT 5.0 section 3.14](https://docs.oasis-open.org/mqtt/mqtt/v5.0/os/mqtt-v5.0-os.html).

No table, schema or RPC change. Sink success means enqueue, not transport flush,
client receipt or PUBACK. Source discovery, accounting and fair scheduler lifecycle
remain app/runtime work; a zero source key permits only pending completion/recovery.

## Failure inventory before implementation

- New work overtakes old exchanges; old recovery loses PacketID/order/metadata;
  busy or a lost admission reply causes same-connection duplicate or wrong DUP.
- A reentrant/concurrent turn sends out of order or retains unbounded bodies.
- Permission changes after preparation but before enqueue; unavailable authority
  ends a valid Session; revocation joins its own admitted scope or silently ACKs.
- Current owner/exchange/options change; malformed/foreign evidence or cancellation
  grants a write. Replaced subscription options rewrite a begun QoS-1 exchange.
- Enqueue fails/panics/returns an unknown result and is retried on the connection;
  cleanup panic leaks a scope or prevents retained pending ending from retrying.
- ACK/renewal races a successful downgraded-QoS-0 enqueue, leading to duplicate
  sends, lost charges, rebased unsent candidates or progress past another charge.
- Original QoS 0 is retried after Busy/expiry or counted as successfully enqueued.
- Sink/app mapping bypasses gateway owner gates, packet order, Receive Maximum,
  property bounds or exact PUBACK binding. End feedback is mistaken for isolation.
- Temporary owner-operation saturation is mistaken for a closed connection.
  Reproduction precedes the Busy classification fix for both delivery QoS levels.

Tests use approved public usecase/metadata/Owners and app integration seams.
Real anchored three-node delivery-to-sink verification retains an explicit
controlled transport sink. Separate real Paho/TCP verifies the gateway sink with
controlled source/window admission. Neither is product listener/process acceptance.

## Frozen context

Source `4ee4ece2104a6cb298a1ac42cea03bfdc337cb22`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/usecase/mqttsession/FLOW.md`: `a74754319cdcaa497dba42a1685e5d10848284f056f59af8303aaf4516e1068c`
- `internal/access/mqtt/FLOW.md`: `0154e8c62703302d362d9b077c0f47c03bb7a33dfcc49bd499bfd14e4b3afdf7`
- `internal/app/FLOW.md`: `38c41dce7a30a729f038861fbab66300cfc7e023e1863bf3984f80bf4ade622e`
- `internal/runtime/mqttsession/FLOW.md`: `c8e693a925f071992ed91cd352db4a7a5a6b9a3b1a5c8667ac6604031770b6de`
- `pkg/db/FLOW.md`: `3560287fef837ef40dcf04754ac1037a28ec85d26dae7748f465ab9524c624fe`
- `pkg/db/meta/FLOW.md`: `39d9e3024bb42cd14587b12557c0d58b9250444c21ceac6598581517096e0079`
- `pkg/channel/FLOW.md`: `e1afae5bebc02c8af1228479ee5cc32686507fe969b6500c27380527de249b01`
- `pkg/protocol/publication/FLOW.md`: `c233c21b41ca6cfc2f4c34ffec50bba4eb099952591856b4f3233f231a2ec655`
