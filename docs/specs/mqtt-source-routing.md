# MQTT source authority routing

Source activation routes to the current Channel leader through the existing
cluster Channel service. The caller supplies the exact expected Channel epoch,
leader epoch and route generation, a server-allocated message ID and stable
positive timestamp. Origin and serving leader read runtime metadata through a
fresh Slot quorum/apply barrier, without append-cache or create-if-missing
fallback. Both revalidate authoritative identity after the operation. The serving
node alone loads the recovered Channel runtime and calls EnsureMQTTSource.
Forwarded requests never forward again and cannot rewrite an expected fence.

Runtime metadata adds a distinct point-read operation to its version-3 codec.
It checks the derived hash/physical Slot, applies a local-only ReadIndex barrier
(or a fresh local noop for narrow embedding ports), reads the row and rechecks
mapping/leadership. It requires current response framing; an older peer cannot
silently downgrade the operation to its ordinary metadata read. Existing read
operations retain their semantics.

Dedicated Channel RPC 93 carries one bounded source request and an exact echoed
request in its reply. A closed status set preserves retryable and terminal
errors without trusting remote text. No format downgrade, redirect chain,
history scan, subscriber fanout or unbounded retry is introduced. The registered
handler follows the stable ServiceGateway across runtime replacement; ordinary
maintenance admission and bounded mutation execution still apply. A completed
source activation can outlive caller cancellation, but cancellation cannot
become a successful subscriber receipt.

Wire v1 uses `WMSQ\x01` / `WMSR\x01`, big-endian fixed-width identities and
uint16-length strings, bounded to 4 KiB. The reply echoes the complete request;
non-success replies require an empty source. Channel IDs are bounded to 1,024
UTF-8 bytes with no NUL. A source requires a canonical nonzero `mqtt-log-v1:`
command identity and `StartAfter < CommittedThrough`. Node source operations
and fresh metadata reads have five-second deadlines. Fresh runtime-meta frames
are bounded before shared decoding; replies permit one row, no collection or
scan cursor, and at most 16 KiB. Startup readiness alone does not prove Slot
quorum has reconverged after restart; callers retain failures until authority
is available and retry under their bounded orchestration.

These APIs establish replicated source protection under current routing, not
receive permission, a source-binding/cursor transaction or SUBACK. The complete
subscription projection still needs those obligations and the future-person
source handshake; shared-copy advancement/transfer and product admission remain
required.

## Failure inventory before implementation

1. Ordinary metadata caching or a reused readiness proof accepts an isolated old
   Slot leader. Presence or absence is returned without a fresh applied barrier.
2. A forged Slot, changed hash table/leader, missing row or mismatched identity
   redirects a source to the wrong authority. Legacy codec fallback strips route
   generation/write fences or removes the fresh-read guarantee.
3. Origin or server overwrites expected epochs, forwards indefinitely, creates
   unknown runtime metadata, or accepts an unavailable/fenced/deleted Channel.
4. Metadata changes, restore/runtime replacement or cancellation during activation
   lets a stale result escape. Missing optional capabilities become local-only
   success; a serving follower can activate storage directly.
5. A malformed, oversized, truncated, trailing, mismatched or unknown-version RPC
   reply is accepted; error results carry a successful source. Peer error strings
   masquerade as known status. The source identity/boundary is unbounded or invalid.
6. Real cross-node operation fails to preserve the first generation, normal
   append ordering or cold recovery; a warmed successful route remains usable
   after Slot quorum is removed. Source protection is mistaken for subscription
   or full product MQTT acceptance.

Tests precede code: deterministic authority/codec failures and three real Node
runtimes with 256 hash Slots, real TCP transport and disk-backed Channel stores.
The Node integration is not process-level product MQTT acceptance.

## Frozen context

Source `2a6fceea2`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/cluster/FLOW.md`: `6ac161946f38f7f4a2f3f870da68d62b3f8008d5653e45dd26707f5b77415bc8`
- `pkg/slot/FLOW.md`: `b15ce935749f0eb4f13dded0bc037727a1773968a158b61e9a699dfaa83a5316`
