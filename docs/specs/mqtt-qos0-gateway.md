# MQTT QoS 0 gateway enqueue and outbound property bounds

SendQoS0 accepts a trusted prepared original publication for the exact connection.
The caller owns current receive permission, source proof, serial preparation and
post-enqueue CompleteQoS0. Entry creates no inflight row, PacketID or ACK binding.
It shares the nonblocking QoS 1 sending gate but does not consume Receive Maximum;
full QoS 1 credit may coexist with QoS 0 enqueue. Success means only enqueue.

Mapping preserves original payload and ordered properties, server identities and
subscription identifier. QoS 0 always has DUP false and PacketID zero, and rejects
expired new delivery with a distinct non-closing result. Native expiry and MQTT/
Will expiry use their original basis and the earlier effective deadline; begun
QoS 1 exchanges remain deliverable with expiry zero. Only a resumed connection
accepts an explicitly redelivered exchange. Write error/panic or cancellation
across enqueue closes/fences the owner; no retry or completion is fabricated.

Generic gateway MQTT adapters may select independent inbound/outbound codec
limits, preserving New's symmetric behavior. App composition reserves bounded
outbound headroom for immutable properties plus server fields (136 properties,
64 KiB property bytes), without raising inbound defaults or the peer packet limit.
No property or payload is truncated. No durable format changes are required.

## Failure inventory before implementation

- QoS 0 allocates a packet identifier, marks DUP, binds ACK or consumes QoS 1 credit.
- Concurrent QoS 0/1 enqueue overlaps or callbacks run under the connection lock.
- A foreign, closed, expired or canceled owner writes; failed writes leave a live
  connection or leak scopes; callback panic exposes contents in diagnostics.
- New expiry is restarted or ignored, expired QoS 0 writes, begun QoS 1 is dropped,
  native/Will expiry is lost, or malformed/overflowing timestamps are accepted.
- Original data is shared/mutated, duplicate properties lost, reserved fields
  forwarded, malformed content sent, or fresh connections accept retransmission.
- Full accepted property blocks fail only because server properties are appended;
  raising outbound bounds weakens inbound limits, peer limits or packet budgets.

Public entry tests, independent generic adapter tests and real Paho/TCP app tests
precede code. This completes an entry prerequisite, not autonomous send/recovery
or product listener acceptance. Those remain required under the full goal.

## Frozen context

Source `3150751579ea0bd54e9963c7f41aff4326b4a13a`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/access/mqtt/FLOW.md`: `864603c225838df451ec59cb42e83a2dfc6eff491c4829afe34c47720a2e12ae`
- `internal/app/FLOW.md`: `af0aae5f861b2967e38c36ebbc99402b04759fb140d159f3f08d89cea049728b`
- `internal/usecase/mqttsession/FLOW.md`: `5de69cd8e7b1535a356185eb912e7e81caddafd00d6b6005af9be4a2686c4c99`
- `internal/runtime/mqttsession/FLOW.md`: `c8e693a925f071992ed91cd352db4a7a5a6b9a3b1a5c8667ac6604031770b6de`
- `pkg/gateway/FLOW.md`: `3fcdf28b4f5dc537c4dbcd0223177217db8ffeec20a6685d9b7890aae14f533e`
- `pkg/channel/FLOW.md`: `e1afae5bebc02c8af1228479ee5cc32686507fe969b6500c27380527de249b01`
- `pkg/protocol/publication/FLOW.md`: `c233c21b41ca6cfc2f4c34ffec50bba4eb099952591856b4f3233f231a2ec655`
