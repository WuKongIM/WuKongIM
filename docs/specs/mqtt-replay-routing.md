# Routed MQTT replay preparation

`Node.PrepareChannelMQTTReplay` exposes bounded preparation through the hosted
Channel service. Node foreground/maintenance gates remain mandatory. Each call
has a five-second bound and uses fresh Slot quorum/apply metadata before and
after work. Exact caller epochs/route, active/creating placement, write fence, leader and
replica/ISR membership must remain valid. Ordinary metadata caches cannot serve
this operation. Logical retention and unrelated lease refreshes do not change
the immutable protected content or require their own retry.

A local leader applies the current runtime metadata before Channel admission.
Otherwise one typed RPC goes to the selected leader. The forwarded request names
that exact serving node; a receiver never forwards it again. A stable gateway
resolves the current service after runtime rebuilds. Returned pages remain local
preparation evidence, not quorum copy receipts, source-release permission or
subscription readiness.

RPC service **94** is a bounded foreground Channel mutation because preparation
may persist a checkpoint and shared copies. Version-1 request/reply magics are
`WMRQ`/`WMRR`; request limit is 4 KiB, reply limit is 16 MiB plus 64 KiB framing.
The complete request (leader, Channel identity, epochs/route, source incarnation,
inclusive range and row/byte limits) is echoed and compared exactly. A reply
contains a closed status and either no data on failure or the two prefix states
and up to 256 owned original-row envelopes on success. Content remains opaque;
MessageDB validates row codecs/content hashes when importing. This wire format
preserves every field and does not establish authority from peer-supplied hashes.

Lengths, versions, counts, UTF-8 identities, source format, range/counter bounds,
requested content budgets and trailing bytes are checked before allocations or
successful return. No old-codec, cached-authority or local-store fallback exists.
No durable table/schema change is introduced. Matching peers must implement this
service before distributed replay preparation can be used.

## Failure inventory before implementation

1. Source/route/leader/epoch changes, write fencing, deleted or malformed placement,
   membership changes, canceled reads or unavailable fresh authority return a
   successful old page. The request is silently rewritten to newer fences.
2. A forwarded call loops, runs on another serving node or survives a cleared
   gateway; Node startup/maintenance gates are bypassed.
3. A reply matches only some request fields, status errors carry data, unknown
   versions/statuses or malformed pages pass, or remote text becomes a sentinel.
4. Oversized counts/lengths allocate before validation, byte budgets omit content,
   truncated/trailing data is accepted, or returned content aliases the RPC frame.
5. Larger-than-request payloads are lost by an incorrect shared 4 KiB cap;
   paging/retries, leader recovery or restart changes original content/digests.
6. Warm isolated leaders bypass fresh quorum checks. A routed page is mistaken
   for copied quorum durability, learner readiness or product MQTT acceptance.

Tests precede code: deterministic routing/codec/admission failures and a real
three-node TCP/disk integration with 256 hash Slots and two physical Slots.

## Frozen context

Source `5dea2b8f1`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/cluster/FLOW.md`: `ef124cd7c0439c158e9846e3108603145dcfc47afbfe7386550aa78accaab37d`
