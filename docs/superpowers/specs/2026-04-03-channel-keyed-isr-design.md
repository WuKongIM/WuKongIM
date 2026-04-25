# Channel-Keyed ISR Runtime Design

## Goal

Remove the independent numeric `GroupID uint64` model from the ISR data-plane stack and make a channel the only business-level replication unit.

The replication stack must stay business-agnostic, so the runtime key is not a `ChannelKey` struct. Instead, the stack uses a strong string type:

```go
type GroupKey string
```

`pkg/storage/channellog` is the only layer allowed to translate a channel identity into a replication key.

## Decision

The repo will move to the following model:

1. `channel` is the only business replication unit
2. `pkg/replication/isr` and `pkg/replication/isrnode` use `GroupKey` as their only instance key
3. `pkg/storage/channellog` removes `ChannelMeta.GroupID`
4. `pkg/storage/channellog` derives `GroupKey` locally from `ChannelKey`
5. No compatibility layer is kept for the old numeric `GroupID`

This is a deliberate breaking change.

## Why The Current Model Is Wrong

The current stack mixes two different identities:

- business identity: `channel`
- replication identity: `GroupID uint64`

That creates an unnecessary controller-assigned mapping layer and conflicts with the intended semantic:

```text
one channel = one ISR
```

As long as the stack keeps a distinct numeric `GroupID`, the real source of truth is still the numeric group, not the channel.

## Chosen Shape

### Replication Key Type

`pkg/replication/isr` defines:

```go
type GroupKey string
```

Rules:

- `GroupKey("")` is invalid
- ISR core treats it as an opaque identifier
- ISR core must never parse business meaning from the key
- equality is exact string equality

This keeps the replication stack generic while still preventing accidental use of arbitrary strings in plain `string` positions.

### Channel To GroupKey Mapping

`pkg/storage/channellog` owns a single normalization function:

```go
func channelGroupKey(key ChannelKey) isr.GroupKey
```

Recommended encoding:

```text
channel/<channelType>/<base64.RawURLEncoding(channelID)>
```

Why this encoding:

- deterministic across nodes
- safe for separators and special characters
- string-only
- opaque to lower layers

The mapping is one-way by design. ISR layers do not need to reverse it.

## Layer Boundaries

### `pkg/replication/isr`

Replace all `GroupID uint64` uses with `GroupKey`.

This includes at least:

- `GroupMeta`
- `ReplicaState`
- `Snapshot`
- `FetchRequest`
- `ApplyFetchRequest`

Semantic rules remain the same except that key validity becomes `GroupKey != ""`.

### `pkg/replication/isrnode`

Replace all runtime instance keys with `GroupKey`.

This includes:

- `Envelope`
- `Runtime`
- `GroupHandle`
- `GroupConfig`
- `GenerationStore`
- registry maps
- scheduler queues
- tombstone indexes
- snapshot waiting queues

The node runtime still manages many ISR instances, but they are keyed by `GroupKey`, not by a numeric id.

### `pkg/storage/channellog`

`ChannelMeta` removes `GroupID`.

All runtime lookups, replay bridges, and log reads derive the ISR key from `ChannelKey` via `channelGroupKey`.

`channellog` remains the only package that knows both:

- business channel identity
- replication runtime identity

That becomes the sole translation boundary.

## Metadata Contract

### `ChannelMeta`

`ChannelMeta` keeps only channel and replication-view fields:

```go
type ChannelMeta struct {
    ChannelID    string
    ChannelType  uint8
    ChannelEpoch uint64
    LeaderEpoch  uint64
    Replicas     []NodeID
    ISR          []NodeID
    Leader       NodeID
    MinISR       int
    Status       ChannelStatus
    Features     ChannelFeatures
}
```

### Epoch Meaning

`ChannelEpoch` means structural channel replication metadata version. It increments when any of these change:

- `Status`
- `Replicas`
- `ISR`
- `MinISR`
- `Features`

`LeaderEpoch` still means leader identity version and may advance independently.

### Removed Rules

The following rules are explicitly removed:

- controller allocates `GroupID`
- `GroupID` is immutable during channel lifetime
- request paths must resolve `ChannelMeta.GroupID` before entering ISR runtime

### New Rules

- request paths still load `ChannelMeta`
- metadata is used for status, epoch, leader, ISR, and feature checks
- runtime lookup key is always derived locally from `ChannelKey`

## Transport And Storage Consequences

### Transport

`pkg/replication/isrnode` transport payloads currently demultiplex by numeric `GroupID`.

After the change, envelopes demultiplex by `GroupKey`.

The wire format should carry the string key directly instead of an 8-byte numeric field. The exact envelope codec can be updated mechanically as part of the refactor because there is no backward-compatibility requirement.

### Generation Store

`GenerationStore.Load/Store` switches from numeric key storage to `GroupKey`.

Generation semantics do not change. They still protect tombstoned instances from stale traffic.

### Message Log

`pkg/storage/channellog.MessageLog` switches from:

```go
Read(groupID uint64, fromOffset uint64, limit int, maxBytes int)
```

to:

```go
Read(groupKey isr.GroupKey, fromOffset uint64, limit int, maxBytes int)
```

Checkpoint replay and idempotency rebuild also use `GroupKey`.

## Migration Strategy

This refactor is done as a direct cutover, not as a staged compatibility migration.

Recommended order:

1. change `pkg/replication/isr` public types to `GroupKey`
2. change `pkg/replication/isrnode` runtime and transport to `GroupKey`
3. change `pkg/storage/channellog` to remove `ChannelMeta.GroupID` and derive `GroupKey`
4. update dependent tests and fakes
5. update design and implementation docs that still describe `channel -> GroupID`

The goal is to let compile errors expose every remaining old-path dependency.

## Rejected Alternatives

### Keep Numeric `GroupID` Internally

Rejected because it preserves the wrong source of truth and leaves the stack conceptually keyed by an implementation detail rather than by the channel.

### Use Plain `string`

Rejected because it weakens type safety and makes accidental misuse easier across package boundaries.

### Expose `ChannelKey` To ISR Core

Rejected because it leaks business semantics into a generic replication library.

## Testing Requirements

At minimum, the refactor must lock the following behavior:

- `pkg/replication/isr`
  - rejects empty `GroupKey`
  - rejects mismatched keys across operations
  - preserves key identity through snapshot and fetch/apply flows
- `pkg/replication/isrnode`
  - `EnsureGroup`, `Group`, and `RemoveGroup` work with string keys
  - tombstone generation fencing still works per `GroupKey`
  - stale or unknown `GroupKey` traffic is dropped
- `pkg/storage/channellog`
  - identical `ChannelKey` values always map to identical `GroupKey`
  - special characters in `ChannelID` do not create collisions
  - send, fetch, status, and checkpoint replay no longer depend on `ChannelMeta.GroupID`
- dependent usecase tests
  - durable send paths work without any external group id

## Impacted Documentation

These documents must be updated to match the new model:

- `docs/superpowers/specs/2026-04-02-channelcluster-design.md`
- `docs/superpowers/plans/2026-04-03-channelcluster-implementation.md`

Any rule that still describes `channel -> GroupID` mapping becomes invalid after this change.
