# internal Flow

## Responsibility

`internal` is the promoted business kernel for the new architecture. It owns
the product entry adapters, entry-agnostic usecases, node-local runtimes,
infrastructure adapters, and the single composition root used by
`cmd/wukongim`. The former v1 server runtime has been removed; new internal
work must stay on the promoted access/usecase/runtime/infra/app boundaries.

The promoted runtime proves the client `SEND -> SENDACK` write path through
`pkg/cluster` and `pkg/channel`. It also exposes legacy-compatible channel,
user, message, conversation, and CMD sync HTTP surfaces backed by cluster Slot
metadata and Channel runtime logs.

Single-node deployment is still a single-node cluster. Do not add send,
storage, or routing branches that bypass cluster semantics.

## Package Boundaries

| Package | Responsibility |
|---------|----------------|
| `app` | Single composition root for config, dependency wiring, and lifecycle. |
| `access/api` | Health, readiness, bench/v1 target HTTP surface, legacy `/route` address lookup, and legacy-compatible channel/user/message/conversation/CMD sync HTTP adapters. |
| `access/gateway` | Gateway event/frame adapter: presence activation/deactivation mapping, `SendPacket` mapping, sendack writing, and entry error mapping. |
| `access/manager` | Manager HTTP adapter for diagnostics and read-only management views. |
| `access/node` | Node RPC adapter for presence, conversation authority, delivery, and channel append calls between internal nodes. |
| `log` | Zap/lumberjack-backed application logger for the internal composition root. |
| `observability/diagnostics` | Bounded node-local diagnostics events, trace indexing, runtime tracking rules, and sendtrace context helpers. |
| `usecase/channel` | Entry-agnostic channel metadata, subscriber, temporary subscriber, allowlist, and denylist orchestration. |
| `usecase/cmdsync` | Entry-agnostic durable CMD offline sync and syncack over CMD-kind conversation projection rows. |
| `usecase/conversation` | Entry-agnostic ordinary recent conversation list, sync, unread, and delete orchestration over normal-kind conversation projection rows. |
| `usecase/delivery` | Entry-agnostic delivery submission and route-to-owner fanout orchestration. |
| `usecase/management` | Entry-agnostic management read orchestration for manager adapters. |
| `usecase/message` | Entry-agnostic SEND facade and compatible channel message sync. |
| `usecase/presence` | Entry-agnostic connection presence activation, deactivation, lookup, and authority coordination. |
| `usecase/user` | Entry-agnostic user token, device quit, online status, and system UID compatibility orchestration. |
| `runtime/conversationactive` | Kind-aware UID-owned active conversation cache and flush runtime. |
| `runtime/delivery` | Node-local online fanout, owner push, and retry runtime. |
| `runtime/online` | Owner-local active gateway session registry used for local delivery and dirty touch batching. |
| `runtime/presence` | In-memory UID route authority directory for hash slots locally led by this node. |
| `runtime/channelappend` | Channel-authority write group where each local authoritative channel is served by an independent single-writer state machine, hash-sharded for lookup and advanced by shared worker pools. |
| `infra/cluster` | Adapter from channel append, channel/user metadata, delivery, presence, conversation, and CMD sync ports to `pkg/cluster` / `pkg/channel`. |
| `contracts/channelmembers` | Stable legacy-compatible member-list channel-id namespace helpers. |
| `contracts/messageevents` | Lightweight committed-message event DTOs for later delivery/conversation migration. |

## Dependency Direction

```text
access -> usecase
usecase -> contracts and usecase-defined ports
infra -> pkg/cluster and pkg/channel, implementing usecase ports
app -> access, usecase, infra, log, pkg composition dependencies including shared pkg/plugin/pluginhost plugin host runtime
```

`internal/usecase/message` must remain protocol- and cluster-agnostic. It
must not import `pkg/gateway`, `pkg/protocol/frame`, `pkg/cluster`,
`pkg/channel`, `internal/access`, or `internal/app`.

## Phase-1 Send Flow

```text
pkg/gateway SendPacket
  -> internal/access/gateway.Handler
  -> internal/usecase/message.App thin facade
  -> internal/runtime/channelappend.Router resolves channel append authority
  -> local channelappend.Group append authority or access/node Channel Append RPC
  -> authority writer validates, assigns message IDs, and appends through infra/cluster.ChannelAppender
  -> pkg/cluster.Node.AppendChannelBatch -> pkg/channel append
  -> internal/usecase/message.SendResult
  -> internal/access/gateway writes SendackPacket
```

Only the channel authority node creates and owns real channel append state. A
non-authority node forwards the batch to the authority node through Channel
Append RPC and does not create proxy channel state or enter a local writer for
that channel. Conversation projection, recipient authority grouping, owner
push, and delivery fanout run after the successful append in the authority
writer's best-effort post-commit pipeline. Conversation admission emits
`conversationactive.ActiveBatch` with an explicit `metadb.ConversationKind`:
ordinary SENDs project `ConversationKindNormal`, while `SyncOnce` or command
channel commits project `ConversationKindCMD`.

## Conversation Projection Flow

```text
ordinary conversation list/sync
  -> internal/access/api conversation routes
  -> internal/usecase/conversation
  -> internal/infra/cluster ConversationStore
  -> ListConversationActiveView(ConversationKindNormal, uid)
  -> read latest non-CMD Channel runtime messages for visible rows

CMD offline sync/syncack
  -> internal/access/api /message/sync or /message/syncack
  -> internal/usecase/cmdsync
  -> internal/infra/cluster CMDSyncStore
  -> ListConversationActivePage(ConversationKindCMD, uid)
  -> read only SyncOnce or command-channel Channel runtime messages
  -> syncack writes ConversationKindCMD read cursors
```

`pkg/db/meta` owns one canonical conversation projection table keyed by
`(uid, kind, channel_id, channel_type)`. Both ordinary and CMD rows are routed
by the UID hash slot, including single-node cluster deployments. Ordinary
conversation storage and listing do not infer semantics from the `____cmd`
suffix; the suffix remains a legacy command-channel naming detail, while
ordinary/CMD separation is carried by explicit `ConversationKind` and the
durable Channel runtime `SyncOnce` marker.

## Phase-1 Presence Flow

```text
pkg/gateway CONNECT activation
  -> internal/access/gateway.Handler
  -> internal/usecase/presence.App.Activate
  -> internal/runtime/online pending route
  -> internal/infra/cluster.PresenceAuthorityClient
  -> local runtime/presence.Directory or access/node RPC to the current authority
  -> internal/runtime/online active route
```

Route-authority changes are observed from `pkg/cluster`. When this node gains
authority for a hash slot, `internal/app` installs the corresponding
`runtime/presence.Directory` authority epoch. Owner gateway PING marks active
sessions dirty in `runtime/online`; the app worker batches those dirty routes,
resolves their current UID authorities, sends `TouchRoutes`, and expires
authority routes by TTL. When leadership moves elsewhere, local authority state
for that hash slot is cleared. Authority changes do not scan or replay all
owner-local active sessions.

## Phase-1 Bench Target Flow

```text
wkbench target preflight
  -> internal/access/api healthz, readyz, bench/v1 capabilities
wkbench capacity discovery
  -> internal/access/api bench/v1 capacity-target
wkbench prepare
  -> internal/access/api benchmark-only setup acknowledgments
wkbench traffic
  -> pkg/gateway WKProto SEND
  -> Phase-1 Send Flow
```

`Send` is only a batch-of-one wrapper. `SendBatch` is the canonical correctness
path so gateway micro-batching and future send runtimes do not grow separate
behavior.

## Top Snapshot Flow

```text
runtime observer events
  -> internal/app top collector
  -> internal/access/api /top/v1/snapshot
  -> wkcli top client-side multi-node aggregation
```

## Diagnostics Trace Flow

```text
manager diagnostics HTTP request
  -> internal/access/manager request validation and permissions
  -> internal/usecase/management diagnostics orchestration
  -> local internal/observability/diagnostics store or access/node Manager Diagnostics RPC
  -> bounded trace/message/event result page or tracking-rule mutation result
```

Diagnostics trace storage and tracking rules belong to `internal`; new v2
manager routes must not import the legacy `internal/observability/diagnostics`
package.

## Legacy Channel Management Flow

```text
legacy /channel* HTTP request
  -> internal/access/api request validation and legacy JSON envelope
  -> internal/usecase/channel
  -> internal/infra/cluster ChannelMetadataStore
  -> pkg/cluster.Node Slot metadata facade
  -> Slot Raft propose for mutations or routed Slot metadata read for list/get
```

Temporary subscribers, allowlists, and denylists use stable internal member-list
channel IDs so data remains compatible with the legacy metadata layout. These
APIs do not bypass cluster semantics; single-node deployment is handled as a
single-node cluster.

## Promoted Entrypoint Boundary

- `cmd/wukongim` is the promoted product entrypoint and wires
  `internal/app` by default.
- Plugin host RPC protobuf wire contracts live in `pkg/plugin/pluginproto`;
  keep field numbers compatible with `github.com/WuKongIM/go-pdk`.
- Plugin host runtime is shared under `pkg/plugin/pluginhost`; `internal/app`
  adapts it to `internal/usecase/plugin`.
- The old v1 server runtime has been removed; do not recreate an
  `internal/legacy` implementation path.
- Controller, the new cluster runtime, and the multi-reactor channel runtime
  are canonical under `pkg/controller`, `pkg/cluster`, and `pkg/channel`.
- Do not implement realtime `NoPersist` delivery yet; return a stable
  unsupported result until that runtime exists.
- Do not advertise legacy message fields that `channel.Message` cannot
  persist or replicate today.
