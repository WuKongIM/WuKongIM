# MQTT outbound acknowledgement orchestration

## Contract

Acknowledge a previously captured outbound exchange identity under the current
exact local Owner and current authoritative Session. The request contains the
source cursor key, PacketID and durable DeliveryOrder. A wire PacketID alone is
not sufficient to execute deferred work: future entry/window composition must
capture the matching exchange before queueing it, and retain that identity across
retries. This usecase does not fabricate that network-send evidence.

One admitted Owner scope may make at most three existing command-70 proposals
after definite CAS rejection. Each attempt rereads the Session/inflight pair; a
retry requires a strictly newer parent revision and the same immutable selected
exchange. One successful ACK releases that exchange and advances only contiguous
completion. The
current subscription need not remain active: ordinary unsubscribe cannot prevent
completion of an already admitted exchange. Session termination or owner loss
still fences it. An absent exchange is an explicit absent observation, not proof
of a delivery or of this call committing. A reused PacketID with a different
order/key is a conflict and must never be acknowledged by the old request.

Exact committed replies must match the selected packet/order and next parent
revision. Unknown write outcomes never retry inside the call; a later call may
reconcile absence. Ordinary parent renewal, accounting or another ACK may justify
a bounded retry only after definite rejection. Neighbor links/update time can
change; source/order/packet/content/QoS/topic cannot. There is no new table,
packet codec, Slot command or shared-content GC permission. App wires
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


## Bounded concurrent-progress failure inventory

Before changing ACK conflict behavior, verify a real Session renewal between the
point read and command-70 proposal. It changes only the parent revision and lease,
not the sent exchange. Treating that definite rejection as an unknown outcome
needlessly closes an otherwise current connection. This deterministic hazard is
separate from the not-yet-diagnosed intermittent process EOF.

Any bounded retry must retain one admitted exact Owner and original immutable
exchange identity, reread authority after a definite rejection, and require a
strictly newer parent revision. At most three proposals share the original
operation deadline. Unknown errors (even an error named Conflict), malformed
receipts, partial reads, unchanged/reversed revisions, changed Owner/UID/lifetime,
reused PacketID/order, changed publication/topic/QoS, cancellation, expiry and
clock regression must stop. Neighbor-link changes from an independently ACKed
exchange may be accepted only with the same immutable selected exchange and fresh
valid metadata. If that selected exchange is already absent, return only Absent
and make no new proposal. Lost successful replies remain unknown for this call;
a later call may observe absence without claiming it committed the original ACK.


## Concurrent-progress verification

The pre-fix real-metadata test rejected ACK after a successful native renewal
between its read and write. Additional pre-fix failures covered a neighbor ACK,
an ACK completing between rejection and reread, and the bounded churn policy.
After implementation, focused race coverage passes these and sixteen stopping
conditions. The metadata write remains the atomic authority for current neighbor
links; retry does not carry stale links or payload bytes into the command.

This is a verified deterministic concurrency fix. The earlier intermittent
product-process EOF remains a separate unconfirmed diagnosis; temporary close-site
and ACK-error probes on the old implementation passed without reproducing it.
Those probes were removed. Exact commands, source hashes and process results are
recorded in [concurrency evidence](../reports/mqtt-ack-concurrency.json).


The candidate passed all eight subscription-interruption process scenarios.
Related unsubscribe regression passed three of four cases; the remaining
three-node group case failed on its initial SUBSCRIBE before any publication or
ACK. Subsequent error-only Subscribe/CAS probes passed three repetitions without
capturing the cause. That setup failure remains open independently of this
verified ACK/renewal fix. No failing assertion was removed or weakened.
