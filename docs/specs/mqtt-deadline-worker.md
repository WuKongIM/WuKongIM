# MQTT deadline worker

One node-owned managed loop schedules Session expiry and Waiting Will advancement.
Business decisions remain in `ReconcileDeadline`; scans supply candidates only.
This worker does not publish detached Ready/executing Wills or renew live owners.

## Failure inventory before implementation

1. Scan every Slot on every node, retain lost-owner cursors forever, or confuse
   physical Raft groups with logical hash Slots. Obtain current locally led hash
   Slots each turn, validate the bounded unique list, and prune lost ownership.
2. A hot Session page starves Waiting Wills or another Slot when the visit budget
   is smaller than a page. Rotate `(hash_slot, index_kind)` after every attempted
   page and advance cursors only past visited rows. Use complete encoded-order
   tuples, including length-prefixed strings and all Will generations.
3. Repeated failures block the earliest page forever. A failed visited candidate
   advances the scan hint; its durable row remains for the next complete pass.
   Scan failures preserve their cursor. Empty/future range boundaries reset it.
4. Skip unstarted rows after cancellation/budget exhaustion, or retain a future
   cursor that hides newly due rows. Stop before unvisited rows; a future deadline
   resets that stream to the beginning for the next fair pass.
5. Detached Ready/executing Wills are mistaken for Waiting work, cancelled, or
   permanently block later Waiting records. Visit them without lifecycle mutation;
   publication belongs to its separate fenced executor.
6. Corrupt, oversized, unordered, duplicate or nonprogressing pages are accepted
   and move a scan hint past missing work. Validate the complete bounded page and
   its continuation before invoking any reconciler. No partial page is trusted.
   A dependency returning nil after its call deadline is not successful evidence;
   check cancellation at return before accepting a Slot list/page or observation.
7. Work creates an unbounded queue, per-session timer/goroutine, or detached
   timeout task. Use one synchronous loop, bounded pages/visits and per-call/turn
   contexts. A dependency ignoring cancellation remains joined; never forget it.
8. Stop returns before work finishes, a timed-out stop permits an overlapping
   restart, or restore restarts with old cursors. Cancel/join one exact run, reject
   Start while it drains, and create fresh cursors only after joined shutdown.
9. Startup inherits an expiring boot context as its lifetime, duplicates loops,
   leaks under repeated Start/Stop, or lacks fixed task ownership. Start checks
   its call context; explicit Stop owns lifetime. Register a fixed optional MQTT
   singleton with the existing goroutine registry.
10. Diagnostics leak UID/ClientID/Channel IDs or error text. Observations expose
    bounded counts and duration only; callbacks must be nonblocking.

## Bounds and scope

Default configuration assumes 256 logical hash Slots, a 200ms interval, a two-
second turn, 250ms per dependency call, eight alternating index pages per turn,
16 rows per page and at most 64 visited candidates. Options expose bounded limits
for composition and pressure validation. A turn never overlaps another turn,
and stores at most two complete cursors per configured hash Slot.

A locally led list is an admission hint, not a lease. Recovery pages and every
reconciler use current Slot authority; ownership changes cannot manufacture
cleanup proof. Failed candidates remain durable. Stop/restore must join this loop
before closing its metadata/usecase dependencies. Product configuration/listener
and full recovery/publication remain separate required work.

## Frozen context

Source `065ff2174`; SHA-256 digests:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/runtime/mqttsession/FLOW.md`: `01488eb8b82a05cb8a03a39c33290f8429cc5a3ddaf2169772021aba5d7f95fc`
- `internal/usecase/mqttsession/FLOW.md`: `a0b7c20fa60f7c56154ecf436d8ec8865a2d18677f2d9eea142699d174489182`
- `internal/app/FLOW.md`: `7fa6de76a3b2ac3501d212d467e022789d09400c3ba059791a5ea83c111a8889`
- `pkg/goroutine/FLOW.md`: `5f0a454e44782554d37b6f8ed9cba1eb1662d7a1ba482320cfb9f9f975d5e36e`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/meta/FLOW.md`: `e05d11e22fc0c4afa0898a9e6498b45b9cc7f594fd4b8529587056cd784ecbb7`
