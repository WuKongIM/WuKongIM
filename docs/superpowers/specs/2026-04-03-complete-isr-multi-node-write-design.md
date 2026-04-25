# Complete ISR Multi-Node Write Design

## Goal

Make channel message send use the real `pkg/storage/channellog` data plane with:

- real multi-node ISR replication
- durable commit-before-ack semantics
- committed message recovery after follower restart
- no dependency on cross-node online delivery

This design only covers message durable write and committed read visibility.

## Non-Goals

- cross-node online fanout
- automatic replica placement
- automatic leader election policy above the existing control plane
- full channel lifecycle orchestration

## Decision

The system will run with two planes composed by `internal/app`.

- Control plane: existing `pkg/cluster/raftcluster` + `pkg/storage/metastore`
- Data plane: `pkg/replication/isrnode` + `pkg/storage/channellog`

The control plane owns authoritative channel replication metadata.
The data plane owns message append, replication, commit, local persistence, and fetch.

`internal/usecase/message` must send through a real `channellog.Cluster`, not through `*raftcluster.Cluster`.

## Architecture

### Control Plane

The control plane remains responsible for:

- channel replication metadata storage
- leader identity view
- ISR membership view
- epoch and feature changes
- leader lease issuance

The control plane must expose an authoritative `ChannelRuntimeMeta` read model with at least:

- `ChannelID`
- `ChannelType`
- `ChannelEpoch`
- `LeaderEpoch`
- `Replicas`
- `ISR`
- `Leader`
- `MinISR`
- `Status`
- `Features`
- `LeaseUntil`

`metadb.Channel` is not sufficient for this purpose because it only stores basic business channel fields and does not include replication state.

### Data Plane

The data plane is built from:

- one local `isrnode.Runtime`
- one local `channellog.DB`
- one local `channellog.Cluster`

Rules:

- one channel maps to one `isr.GroupKey`
- `channellog` remains the only package allowed to translate `ChannelKey -> GroupKey`
- `channellog.Cluster` only caches channel metadata and executes channel-facing send/fetch/status
- `isrnode.Runtime` owns group lifecycle and replication scheduling

## App Composition

`internal/app` will own the only composition root.

It must construct:

- `channelLogDB *channellog.DB`
- `isrRuntime isrnode.Runtime`
- `channelLog channellog.Cluster`
- `channelMetaRefresher`
- `channelMetaApplier`

The metadata apply sequence is fixed:

1. load authoritative control-plane metadata
2. project it into `isr.GroupMeta`
3. `EnsureGroup` or `ApplyMeta` on `isrnode.Runtime`
4. project it into `channellog.ChannelMeta`
5. `ApplyMeta` on `channellog.Cluster`

`channellog.ApplyMeta(...)` does not create or update ISR groups by itself and must not be treated as a runtime lifecycle hook.

## Message ID Strategy

### Decision

`MessageID` will use `github.com/bwmarrin/snowflake`.

Each node process creates one local snowflake generator at startup.

`channellog.Config.MessageIDs` will be backed by that generator.

### Constraints

V1 uses direct node-id mapping:

- app `Node.ID` maps directly to the snowflake node id
- startup must validate that `Node.ID` fits the supported snowflake node-id range `0..1023`
- if the configured node id is outside that range, app startup must fail fast

This is a deliberate V1 constraint because silent hashing or truncation would risk collisions.

### Guarantees

The goal of `MessageID` is cluster-wide uniqueness, not global total ordering across all nodes.

`messageSeq` remains the per-channel ordered value exposed to clients.

## Metadata Contract

`internal/usecase/message.MetaRefresher` must read authoritative control-plane metadata and return a full channel replication view.

Projection rules:

- `ChannelRuntimeMeta -> channellog.ChannelMeta`
  - copies business-facing replication fields
- `ChannelRuntimeMeta -> isr.GroupMeta`
  - copies replication fields
  - maps `LeaseUntil` into ISR leader lease state

`LeaseUntil` must come from the control plane.
The send path must not invent or extend leader lease locally.

### Metadata Distribution

Request-scoped metadata refresh is not sufficient for multi-node ISR readiness.

Every replica node listed in `ChannelRuntimeMeta.Replicas` must have local metadata and local ISR group lifecycle applied before it can participate in replication.

V1 requires a node-local metadata sync path in `internal/app`:

- preload all channel runtime metadata whose replica set includes the local node during startup
- reconcile local applied metadata when the control-plane metadata changes or on bounded periodic refresh
- ensure follower groups are created and updated before the leader depends on them for commit

`message.MetaRefresher` remains a request-path repair mechanism for stale local metadata, not the primary metadata distribution mechanism for the data plane.

## Write Path

### Request Path

The durable send path is:

1. access layer builds `message.SendCommand`
2. `message.App.Send(...)` calls durable send with request context and bounded timeout
3. `channellog.Send(...)` validates metadata, status, protocol capability, and idempotency
4. leader local ISR replica appends the encoded message record
5. ISR replication advances follower progress
6. once ISR progress reaches the commit condition, leader `HW` advances
7. append waiter is released
8. `channellog.Send(...)` returns `MessageID` and committed `MessageSeq`
9. access layer writes send ack

### Commit Semantics

Ack is only allowed after commit.

The lock-in rule is:

```text
messageSeq = committed HW
```

Leader local append is not enough to ack success.

### Context And Timeout

`internal/usecase/message` must stop using `context.Background()` for durable send.

It must use:

- request-scoped context from the access layer, or
- a bounded timeout derived from request context

Reason:

- if quorum is not satisfied, ISR append may wait for replication progress
- that wait must be cancellable

This requires plumbing context from access ingress down into the durable send path.

V1 must not recreate an unbounded background context inside the usecase.

## Replication Transport

### Required `isrnode` Core Extension

The transport adapter alone is not sufficient with the current `pkg/replication/isrnode` surface.

Today the runtime can originate replication work and consume fetch responses, but it does not expose a production serving path for inbound fetch requests.

V1 must add a narrow core extension in `pkg/replication/isrnode` for serving inbound replication fetches:

- accept a fetch request addressed by group key, generation, epoch, and follower identity
- validate local ownership and generation fencing
- execute the local replica fetch path
- return a fetch result payload for the transport adapter to encode and send

The rule is:

- fetch semantics and group/generation validation stay in `pkg/replication/isrnode`
- wire framing and network transport stay in the adapter package

### Required Production Adapter

The repository needs a concrete production adapter for `pkg/replication/isrnode`.

Create a dedicated adapter package built on top of `pkg/transport/nodetransport`.

Responsibilities:

- implement `isrnode.Transport`
- implement `isrnode.PeerSessionManager`
- define the concrete wire codec for ISR replication envelopes and responses
- use the narrow `isrnode` fetch-serving extension for inbound replication requests
- isolate data-plane traffic from existing raft traffic

This adapter must stay outside `pkg/replication/isrnode` so the core runtime remains generic.

### Transport Rules

- data-plane transport uses its own message types or RPC payloads on shared node transport
- replication traffic must not reuse control-plane raft payload semantics
- per-peer session reuse and backpressure remain the responsibility of the transport adapter
- V1 must not assume a second independent `nodetransport.Server.HandleRPC(...)` registration slot on the shared server; the existing control-plane runtime already owns that slot today
- if shared RPC is used, the implementation must introduce an explicit RPC multiplexer; otherwise prefer dedicated data-plane message types on the shared server

## Channel State Storage

`pkg/storage/channellog` already has real message log persistence and checkpoint/snapshot bridges.
V1 still needs a real `ChannelStateStore` and `StateStoreFactory` for:

- idempotency index reads
- idempotency index updates on committed apply
- snapshot restore

The app must not rely on test fakes for channel state storage.

## Failure Semantics

### `ErrStaleMeta`

Return when:

- local channel metadata is missing
- local runtime group is missing for known channel metadata
- request expected epoch does not match local applied metadata

Handling:

- refresh authoritative metadata
- apply projections to `isrnode` and `channellog`
- retry once

### `ErrNotLeader`

Return when:

- local group role is not leader
- local leader lease has expired
- local leader fencing is observed by ISR

Handling:

- refresh metadata
- retry once only if refreshed metadata makes local node leader again

### Quorum / Timeout

Two failure classes must stay distinct:

- immediate insufficient ISR: metadata view already shows `len(ISR) < MinISR`
- replication timeout: metadata looks valid but commit cannot finish before request deadline

The second case must fail by request timeout, never by false success.

## Testing Strategy

### Layer 1: ISR / Data-Plane Contract Tests

Lock the following behavior:

- leader append does not ack before commit
- follower replication advances leader commit
- lease expiry fences leader writes
- divergence and truncate recovery work
- idempotent replay returns the original `MessageID` and `MessageSeq`

### Layer 2: Three-Node `channellog` Integration

Run a real three-node ISR harness with:

- node 1 leader
- node 2 and node 3 followers
- one channel metadata view applied on all nodes

Lock the following behavior:

- leader `Send` only returns after commit
- committed message becomes readable from `channellog.Fetch`
- committed message remains recoverable after follower restart
- quorum loss causes timeout or insufficient-ISR failure, not success

### Layer 3: Three-Node `internal/app` Integration

Run three real app instances and lock:

- gateway send against leader returns success only after durable commit
- acked message exists in `channellog` storage
- stale metadata or old leader paths return the expected retry/not-leader semantics

Cross-node online fanout is explicitly out of scope for this phase.

## Implementation Order

1. add real `ChannelStateStore` and `StateStoreFactory` to `pkg/storage/channellog`
2. add snowflake-backed `MessageIDGenerator`
3. add authoritative control-plane `ChannelRuntimeMeta`
4. add the narrow `isrnode` inbound fetch-serving extension
5. add production `isrnode` transport/session adapter on top of `nodetransport`
6. add node-local metadata preload and reconcile in `internal/app`
7. wire `isrnode.Runtime` + `channellog.Cluster` into `internal/app`
8. switch `message.App` durable path to request-scoped context and real `channellog.Cluster`
9. add three-node integration coverage

## Completion Criteria

This phase is complete when all of the following are true:

- a three-node cluster performs real ISR replication for message send
- send ack is returned only after commit
- acked messages are readable from `pkg/storage/channellog`
- follower restart preserves committed messages
- `ErrStaleMeta`, `ErrNotLeader`, lease-expired, and quorum-timeout semantics are deterministic
