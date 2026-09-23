# MQTT replay preparation through Channel admission

The Channel facade prepares a bounded shared replay page on an already recovered
leader. It reuses append admission authority, lookup-waiter lifecycle ownership
and the checkpoint worker pool. Reactor goroutines perform no storage I/O.
Explicit Channel/leader epochs and route generation remain unchanged from caller
to completion. A caller-selected upper range cannot exceed reactor-captured HW.

The worker persists only that captured HW, confirms the committed source
generation, and uses an optional store port to prepare the page. Temporary store
leases close on every outcome. The adapter preserves complete opaque original
row envelopes and all prefix fields without decoding product payloads. Pages own
their bytes, contain at most 256 records and 16 MiB, and never advance source
release. A previously copied portion returns an existing bounded page before
continuing into uncopied content; a lost short-page reply can be retried safely.

Preparation establishes replica-local immutable content. It is not a quorum
copy receipt, delivery permission or SUBACK. Fresh cluster routing, distributed
copy decisions and follower/learner transfer must surround this primitive before
product use. Unsupported/legacy stores fail explicitly; no fallback bypasses
Channel authority or cluster semantics.

## Failure inventory before code

1. A follower, unrecovered leader, stale epoch/route, write fence, canceled guard
   or legacy runtime admits preparation or publishes a late successful result.
2. Caller-selected future HW, mismatched source incarnation, unactivated source,
   malformed result boundaries/counts/bytes or an unbounded range escapes checks.
3. Copy/fsync runs in the reactor or foreground read pool, a temporary lease
   leaks on failure/panic, or cancellation/eviction forgets outstanding work.
4. Message/source completions consume a replay waiter; foreign operations or
   stale workers complete a newer request. Shutdown strands an admitted future.
5. Lost short-page replies, concurrent preparation, appends or reopen produce
   inconsistent content, reset the source boundary or advance source release.
6. Adapter conversion loses native metadata, aliases leased bytes or hides
   storage corruption. Unsupported stores appear to prove usable content.
7. A prepared page is mistaken for replicated copy durability or complete MQTT
   acceptance. Tests of the primitive cannot establish product readiness.

Deterministic failure tests and real quorum-runtime/disk integration precede
implementation. Storage fault isolation supplements integration for corruption
that public endpoints cannot deliberately create.

## Frozen context

Source `330690f90`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `f577027836c36d28732ca805bf835e857957b778b064e81f92cda44b6a344d0c`
- `pkg/channel/reactor/FLOW.md`: `dd6fe5b33b5e9cff753b9acbe66ce6e18a6eb4fae9ebb715cd5f92d691319bb4`
- `pkg/channel/worker/FLOW.md`: `6962a5c461718df0e5c8c0500fe4c8da8ea670f29a58d89e3a3baa39d149f49e`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/message/FLOW.md`: `5dea0095affde3e22b98dd1a1837687f8a83341e9122520da4b86e9745a1559a`
