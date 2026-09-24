# MQTT anchored consumer reads

Delivery and accounting need bounded reads of immutable shared content after
ordinary history has been removed. Repair export cannot serve this path: it
requires every page to reach an anchor endpoint. Consumer pages may stop early,
but must still be covered by an independently committed anchor and locally
verified complete shared prefix. Replica-local copy-ahead is not admission.

## Failure inventory before implementation

- An absent/pending/foreign anchor, copy-ahead or incomplete replica coverage
  authorizes a consumer page, or a caller-supplied tail exceeds the anchor.
- Pagination must read the entire anchored prefix, ignores row/byte bounds, or
  interprets a missing covered row/meter as an empty page.
- Native trimming, restart or content edits change the immutable payload,
  publication metadata, message identity or content hash used for redelivery.
- A corrupted anchor endpoint, prefix digest, canonical row or local checkpoint
  is ignored; a partial response escapes after a later row fails validation.
- Reads mutate source/cursor/replay state, grant GC or bypass cluster authority.

## Contract

One pinned storage snapshot verifies the requested committed anchor, exact source
incarnation, local coverage and its full anchored prefix endpoint. It then returns
at most 256 rows and 16 MiB of canonical content for the requested positive range,
which must lie within that anchor. Short pages retain their own Before/After
prefixes; repair export still requires the complete endpoint. No history fallback,
filtering, accounting, authorization or mutation happens in storage. Result bytes
are independently owned. The Channel store adapter exposes the same bounded port
without leaking MessageDB codecs into usecases.

Foreground Node/RPC routing now exposes this storage contract. Typed content
interpretation, per-subscription qualification/accounting, window admission,
PUBACK and product delivery remain required. A content read grants no consumer
authorization. No schema or command format changes; RPC 102 requires matching peers.

Tests use the already approved storage, Channel adapter, Node and app integration seams.

## Frozen context

Source `7913d76f2fe7f6a62929f723885b170f2db14a16`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `3560287fef837ef40dcf04754ac1037a28ec85d26dae7748f465ab9524c624fe`
- `pkg/db/message/FLOW.md`: `de30b69972ef9e9446bc781fdb12d150cc92c90c526cb3d4b1af816c6ba863f1`
- `pkg/channel/FLOW.md`: `84b7fa1e43bf3e829f32f0f0f9b8ac6572dc26f4ed6642233feb1929d02807d3`

- `pkg/cluster/FLOW.md`: `d17ed4ac808be6d063879556da108c7d85f0d7410412f709999f43680d72a4a4`

- `internal/app/FLOW.md`: `c0be903274f63b6d7f303b9fe7d8155d548376772babeccc2be10887fa0d152f`

## Routed read extension

The foreground Node facade routes once to the current Channel leader, with fresh
Slot placement and stable-fence checks before and after the read on both origin
and server. Storage I/O has a separate bounded serving pool (four requests, no
wait queue), not a reactor goroutine. RPC 102 uses distinct version-1 magic and
an exact anchor/request echo around the existing bounded replay page codec.
Older nodes reject it; there is no fallback to local storage or ordinary history.

Additional failures: wrong serving node, route/ISR/fence changes, missing port,
backpressure, canceled calls, foreign replies, malformed/trailing/oversized RPC
bytes or lost foreground admission cannot return a successful page. Real three-node integration reads the same anchored
short page through two remote origins after original trim, under a stable fence.
It verifies chained pagination, caller byte ownership and missing-anchor rejection.
