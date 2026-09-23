# MQTT authoritative Slot access

This connects the approved MQTT storage commands to distributed metadata access.
It does not implement connection-owner isolation or enable the product listener.

## Contract and failure inventory before implementation

Session-owned state routes by a versioned, length-delimited namespace/ClientID
identity, never UID, generation or connection. Source bindings route by the
existing source Channel ID or inbox UID; Channel type and source incarnation
remain in row identity. Recovery scans explicitly select a logical hash Slot,
whose current physical Slot ownership must be verified. Default topology is
256 hash Slots, independently of the number of Raft groups.

Reads establish a fresh local Slot ReadIndex/durable-apply barrier (a local-only
committed noop remains the embedding fallback), then use one pinned metadata
snapshot for primary and secondary-index reads. Slot mapping, table version and
leader identity must still match when returning. Missing rows are authoritative
only in that view. No fallback may serve a convenient stale replica.

The read protocol is closed and versioned, with bounded requests, responses and
pages. Point reads and pagination preserve exact identity and complete cursors.
Writes use existing commands 67–73 and require a returned deterministic result;
missing result support, fenced hash Slots, malformed results and transport errors
cannot be reported as successful CAS. Product activation must still establish
compatible participants and fenced runtime ownership before invoking these APIs.

Failure cases:

1. Tuple ambiguities, UID-based routing or generation changes split one Session;
   source/UID projection writes land on the Session Slot or include leader epochs.
2. A remote read answers from local absence, omits its fresh apply barrier, mixes
   index/row revisions, caches a negative result, or survives a changed route.
3. Wrong hash-Slot/physical-Slot ownership, unknown operation/version, ambiguous
   fields, oversized work or incomplete cursors reaches storage unchecked.
4. A stale owner mutates children, a missing proposal result becomes success,
   malformed apply results are accepted or uncertain writes become fresh retries.
5. Recovery pages skip equal-deadline/order ties, exceed count/byte bounds, expose
   mutable payload aliases or are mistaken for publication/retention permission.
6. Local snapshot reads accidentally write, retain closed engine views, or change
   existing native table reads. RPC registration/catalog omissions break routing.
7. Node facades bypass foreground maintenance gates, fail to register on real
   transport, lose committed state after Slot leader transfer/restart, or serve
   stale state without a quorum. Validate with a three-node, 256-hash-Slot
   integration test before adding the Node entrypoints.

## Implemented wire and routing contract

- Session routing key is `mqtt-session-v1:` plus lowercase SHA-256 of the ASCII
  domain `mqtt-session-v1:`, big-endian uint16 namespace byte length, namespace
  UTF-8 bytes, big-endian uint16 ClientID byte length, and ClientID UTF-8 bytes.
  Both identity components are bounded to 1,024 bytes. Changing this derivation
  requires migration; connection, owner and lifetime generations never enter it.
- Channel binding owner ID is `<canonical decimal uint8 type>:<ChannelID>`;
  type must be nonzero. Only ChannelID routes to existing metadata authority.
  Embedded colons in ChannelID survive. UID owners route by the exact UID.
- Read service is `RPCSlotMQTTMetadata = 91`, JSON format 1. It carries exact
  physical Slot, logical hash Slot and the closed `MQTTRead` union (kinds 1–15).
  Replies echo the query and routing identity and require a present result on
  success, including authoritative absence. Unknown fields, trailing JSON,
  unsupported versions and unrelated operation fields are rejected.
- Requests are at most 64 KiB and replies 8 MiB; ordinary pages allow 1–64 rows,
  Will recovery allows 1–16. Point reads have no pagination fields. Continuation
  cursors preserve all index tie-breakers. As in existing table scans, a final
  page may retain its input cursor; only `done=false` requires advancement.
- Session-scoped child reads include the current Session from the same snapshot;
  an older child's lifetime must still be compared by the caller. Detached Will
  reads can return content without a current Session. Returned content is owned.
- `cluster.Node` facades use the existing promoted Slot proxy/transport, reject
  foreground calls while stopped or in maintenance, and retain normal proposal
  admission. The read-only transport service follows caller cancellation.
- Commands 67–73 are unchanged. The proxy requires the result-capable proposal
  port before writing, bounds/decodes committed results, preserves CAS conflicts
  and window-full flow control, and maps migration-fenced apply results to stale
  metadata. Applied/exact-retry revisions must match the submitted operation.
- A format probe reports parser support only. It is not replica-set activation,
  owner isolation, restore fencing, authorization or permission to enable MQTT.

## Frozen context

Source `9f031e9ae`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/meta/FLOW.md`: `d99d94311f4a1daa88a1b11377be34250a66e2b545899563c6d157fd61d57ce6`
- `pkg/slot/FLOW.md`: `062638f735ebee4bdf870be9014782454ed75374a7743bb7c7c9fc9925b7cb81`
- `pkg/cluster/FLOW.md`: `a7ee279dba358f1bf9ed5d1a7f9e213f890a4fd5c26d6276e79f699a3cf5b3ec`
