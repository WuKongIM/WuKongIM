# MQTT outbound acknowledgement orchestration

## Contract

Acknowledge a previously captured outbound exchange identity under the current
exact local Owner and current authoritative Session. The request contains the
source cursor key, PacketID and durable DeliveryOrder. A wire PacketID alone is
not sufficient to execute deferred work: future entry/window composition must
capture the matching exchange before queueing it, and retain that identity across
retries. This usecase does not fabricate that network-send evidence.

One authoritative Session/inflight point read and at most one existing command-70
ACK commit release the exchange and advance only contiguous completion. The
current subscription need not remain active: ordinary unsubscribe cannot prevent
completion of an already admitted exchange. Session termination or owner loss
still fences it. An absent exchange is an explicit absent observation, not proof
of a delivery or of this call committing. A reused PacketID with a different
order/key is a conflict and must never be acknowledged by the old request.

Exact committed replies must match the selected packet/order and next parent
revision. Lost replies can be reconciled by re-reading; there is no retry loop,
new table, packet codec, Slot command or shared-content GC permission. App wires
foreground Node access and the same local Owners registry. The [gateway binding](mqtt-outbound-gateway.md) now supplies exact sent identities
and PUBACK dispatch at the internal entry seam. Product scheduling/listener
composition and complete recovery remain required.

## Failure inventory before implementation

- Foreign source/session/owner keys, changed UID, expired/fenced connection,
  missing/ended Session, canceled caller or regressed clock reaches a mutation.
- A partial/extra/malformed read or corrupt inflight is treated as an empty page;
  another PacketID, source key or DeliveryOrder releases the selected exchange.
- Unsubscribe or receive revocation rejects completion of an existing exchange;
  out-of-order PUBACK crosses an earlier outstanding gap or double-frees counters.
- A parent revision race is automatically retried against different work, or a
  malformed success reply claims completion for a foreign order/revision.
- Lost commit replies cannot resume; duplicate/missing exchanges cause writes,
  allocation, new cursors, body copies or false completion claims.
- A callback panic or timeout leaks an admitted owner operation; late work mutates
  a replacement Session; response assembly ignores owner expiry/cancellation.

Tests use the approved public usecase with real metadata and Node/app three-node
integration seams. Admission remains controlled until delivery is implemented.

## Frozen context

Source `de769a11c2baf4999101d8896c32a98707c1a837`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/usecase/mqttsession/FLOW.md`: `5bdb7762ed3d6bbf13d4d0d6e739ac1446729daf69b677a1f9d272884bfea146`
- `internal/app/FLOW.md`: `5104d73a588c20dbd972009953e95eb0c552852ea9a12e253876f1f56b562a71`
- `internal/access/mqtt/FLOW.md`: `81148a6ef00c8b03f3d099d08ed6dff63643686f2fdfa2fc8e506d19535d6766`
- `internal/runtime/mqttsession/FLOW.md`: `c8e693a925f071992ed91cd352db4a7a5a6b9a3b1a5c8667ac6604031770b6de`
