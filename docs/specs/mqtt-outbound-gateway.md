# MQTT outbound gateway exchange binding

## Contract

The entry sends an already durably admitted QoS 1 exchange with a trusted typed
original replay publication. The caller must establish current receive permission,
source commitment/content proof and window admission. Entry checks their structural
association, binds PacketID to exact cursor/order before serialized gateway enqueue,
and forwards PUBACK to the existing entry-neutral acknowledgement usecase.
This is not product delivery scheduling or a new durable receipt.

Each connection retains only bounded identity bindings (no payloads), at most the
lesser of Receive Maximum and the durable 1024-exchange hard limit. One nonblocking
send gate preserves increasing DeliveryOrder; concurrent send attempts yield without
an additional queue. A connection never retransmits an already attempted order.
On resumed connections the caller supplies original exchanges in order with DUP.
PUBACK including a negative reason completes that exchange. Unknown IDs are ignored
under owner admission without storage writes or quota creation. Callback failure,
invalid ACK evidence, or write failure closes/fences; durable recovery stays intact.
An exact binding is captured before invoking any deferred business operation.
A malicious duplicate wire PacketID after legitimate reuse cannot identify its old
order; this adapter does not claim that impossible on-wire distinction.

Ordered application properties and original expiry basis survive mapping. Reserved
server properties are generated from the original message. Expired begun exchanges
still complete with remaining expiry zero; mapping does not abandon them. Size errors
never truncate content. Entry does not advertise unfinished subscription capability.

Protocol reference: [OASIS MQTT 5.0](https://docs.oasis-open.org/mqtt/mqtt/v5.0/os/mqtt-v5.0-os.html),
sections 4.3.2, 4.4 and 4.9: reconnect-only retransmission, negative PUBACK completion,
and connection send quota. Product listener/recovery acceptance remains pending.

## Failure inventory before implementation

- Foreign owner/gateway/session/source/order/hash/message or internal content is sent.
- Data is sent before open, after fence/expiry/cancel, or with absent ACK composition.
- Receive Maximum is exceeded, lower reconnect limits erase old durable work,
  send concurrency reorders exchanges or duplicates same-connection transmission.
- PUBACK races enqueue before binding, reenters a held lock, or targets only PacketID
  instead of captured cursor/order; unknown/duplicate ACK grows credit or writes.
- ACK error/panic/malformed evidence frees binding; late completion removes reuse;
  negative PUBACK causes forbidden retransmission or unnecessary authorization checks.
- Metadata drops duplicates, leaks reserved properties, shares payload storage,
  restarts expiry or abandons a begun expired exchange; oversized output truncates.
- Write/callback panic leaks owner scopes or leaves an apparently live connection.
- A gateway close while sending hangs on its own operation or uses a transport bypass.

Tests precede code at the approved public entry seam and real gateway/Paho + routed
Slot app integration seam. Controlled window admission is explicitly identified;
these are not process-level MQTT acceptance.

## Frozen context

Source `7a072161c26d6ad9e45946275e300bf070fc0527`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/access/mqtt/FLOW.md`: `81148a6ef00c8b03f3d099d08ed6dff63643686f2fdfa2fc8e506d19535d6766`
- `internal/app/FLOW.md`: `a075b90db8140745924041235aeba8a2d657dfb44c1dcefd4d6a14ae768d90bb`
- `internal/usecase/mqttsession/FLOW.md`: `61311c61e2a227527e37437badf4e31d0f3a26f7c11107b84378d904c9da0fcd`
- `internal/runtime/mqttsession/FLOW.md`: `c8e693a925f071992ed91cd352db4a7a5a6b9a3b1a5c8667ac6604031770b6de`
- `internal/contracts/mqttsession/FLOW.md`: `ae9101a196195e04bf9e45c9c5242b0de9c17bb334dd80d9d1fb46764b534424`
- `pkg/gateway/FLOW.md`: `3fcdf28b4f5dc537c4dbcd0223177217db8ffeec20a6685d9b7890aae14f533e`
- `pkg/channel/FLOW.md`: `e1afae5bebc02c8af1228479ee5cc32686507fe969b6500c27380527de249b01`
- `pkg/db/meta/FLOW.md`: `d2687858c0fa93634a91b3d9d24e0dbef5915e1b103b1d7126a62d738a9cf593`
- `pkg/protocol/publication/FLOW.md`: `c233c21b41ca6cfc2f4c34ffec50bba4eb099952591856b4f3233f231a2ec655`
