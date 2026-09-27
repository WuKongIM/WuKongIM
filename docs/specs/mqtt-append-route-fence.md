# Prepared append authority

The approved future-person inbox admission must eventually bind preparation to
the actual append incarnation. This slice carries an optional exact route fence
through the existing append contracts, cluster routing and durable sequencer.
It is not itself an inbox checkpoint receipt or an automatic admission hook.

Failure inventory at the existing metadata, append, RPC and adapter seams:

- A prepared request must not adopt a newer route at origin, serving node,
  queued flush or retry. Epochs alone cannot detect a changed directory.
- Nonzero route fences require complete epochs, quorum commit and fresh Slot
  metadata. Missing strong-read or durable-quorum capability must fail closed.
- Ordinary append caches and create-if-missing cannot authorize this request.
  Absent, deleted, write-fenced, malformed, foreign or changed metadata must not
  reach append. Cancellation and fresh-read failures preserve their errors.
- Single and batch requests must retain the fence through adapter conversion,
  forwarding and single-to-batch mapping. An old wire encoding must reject loss;
  ordinary unfenced requests retain their existing version and bytes.
- Metadata advancement while work is queued must fail the old request without
  upgrading its expected durable authority. Backpressure cannot erase its fence.
- Person directory deletion must advance its existing route fence atomically;
  overflow must reject deletion, and monotonic updates cannot bypass the bump.
- Binary snapshots must retain existing route/directory fields. Physical runtime
  deletion, restore activation and product initial-subscription races still need
  their separate incarnation and owner-isolation contracts before activation.

These checks reuse approved append/storage boundaries. They do not add business
policy to reusable runtime or infrastructure layers. Product MQTT stays disabled
until full admission, recovery, lifecycle and process E2E acceptance are complete.

## Implemented boundary

`ExpectedRouteGeneration` is optional on internal batch and reusable single/batch
append DTOs. A nonzero value requires both epochs, quorum commit (including its
zero/default spelling), a fresh Slot point-read capability, and a durable quorum
runtime. Origin and serving node each take a fresh read; neither creates missing
metadata nor retries against a replacement authority. A five-second context
bounds the opt-in cluster operation. Context-aware runtime activation uses bounded
lock admission; a newer cached floor may reject old work, never rewrite its proof.
The reactor checks the exact installed quorum authority at admission and flush.
Metadata replacement invalidates queued requests; caller cancellation still does
not revoke durability already admitted to the quorum log.

Only fenced append requests select codec **12**. It appends one positive uvarint
route generation after the complete version-11 request body. Existing publication,
expiry and flag semantics keep their original version thresholds. Replies and
ordinary requests stay on codec 11; fenced forwarding rejects legacy success replies. Unrelated message kinds reject version
12. Fenced forwarding sends once with no compatibility downgrade. Older encoders
reject nonzero fences instead of dropping them.

Person business-channel deletion increments both directory generation and the
existing runtime route in one Slot batch, retaining the runtime row. Repeated
business deletion does not increment again. Runtime upserts also include directory
incarnation in route change detection; route overflow returns conflict, including
migration mutations using the same reducer. Formats and snapshots are unchanged;
all writers must match before depending on this stronger semantic fence.

Deterministic tests cover malformed/stale/cancelled fresh authority, warm-cache
bypass, serving-node rechecks, single/batch conversion, strict wire round trips,
old-peer rejection and atomic deletion/overflow. A controlled blocked-commit
integration proves queued old work cannot adopt new authority. A real 256-hash-Slot
single-node app integration explicitly prepares both directory phases, appends,
deletes/recreates that business directory without changing either epoch, rejects
the old proof and verifies old/new bodies plus absence of the rejected message by
committed indexed reads. Authority installation may add an internal log barrier;
business sequence numbers need not be adjacent across the route change.

Automatic checkpoint-to-append binding remains unfinished. These tests do not
prove atomicity between concurrent Slot deletion and an already admitted Channel
commit, or safety after physical runtime deletion/restore reuses older metadata.
Those lifecycle/activation boundaries, initial inbox subscription handshake,
matched replica capabilities and process E2E acceptance remain required. The
product listener stays disabled.

## Repeatable validation

From the repository root, with matching source and tooling:

```sh
GOWORK=off go test -race -p 2 ./pkg/channel/service ./pkg/channel/reactor ./pkg/cluster/channels ./internal/contracts/channelappend ./internal/infra/cluster ./pkg/db/meta ./pkg/slot/fsm -count=1
GOWORK=off go test -tags=integration -race -p 2 ./internal/app ./pkg/channel/reactor -run '^TestMQTTPreparedAppendRejectsRecreatedDirectorySingleNodeCluster$|^TestAppendRouteFenceQueuedWorkCannotAdoptNewAuthority$' -count=1 -v
GOWORK=off go run ./scripts/flowcheck --mode check
```

The integration tests emit `mqtt_prepared_append_evidence` and
`append_route_queue_evidence` after checking durable results. These are package
integrations, not product process E2E or load acceptance. The source rule freeze
for this slice was HEAD `020ccd4e9`; no complete MQTT activation is claimed.
