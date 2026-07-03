# internalv2 Flow

## Responsibility

`internalv2` is a parallel business kernel for the new architecture. Phase 1
keeps the existing `internal` production path unchanged and proves the client
`SEND -> SENDACK` write skeleton through `pkg/clusterv2` and `pkg/channelv2`.
It also exposes legacy-compatible channel, user, message, conversation, and CMD
sync HTTP surfaces backed by clusterv2 Slot metadata and ChannelV2 logs.

Single-node deployment is still a single-node cluster. Do not add send,
storage, or routing branches that bypass cluster semantics.

## Package Boundaries

| Package | Responsibility |
|---------|----------------|
| `app` | Single composition root for config, dependency wiring, and lifecycle. |
| `access/api` | Health, readiness, bench/v1 target HTTP surface, legacy `/route` address lookup, and legacy-compatible channel/user/message/conversation/CMD sync HTTP adapters. |
| `access/gateway` | Gateway event/frame adapter: presence activation/deactivation mapping, `SendPacket` mapping, sendack writing, and entry error mapping. |
| `access/manager` | Manager HTTP adapter for diagnostics and read-only management views. |
| `access/node` | Node RPC adapter for presence, conversation authority, delivery, and channel append calls between internalv2 nodes. |
| `log` | Zap/lumberjack-backed application logger for the internalv2 composition root. |
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
| `infra/cluster` | Adapter from channel append, channel/user metadata, delivery, presence, conversation, and CMD sync ports to `pkg/clusterv2` / `pkg/channelv2`. |
| `contracts/channelmembers` | Stable legacy-compatible member-list channel-id namespace helpers. |
| `contracts/messageevents` | Lightweight committed-message event DTOs for later delivery/conversation migration. |

## Dependency Direction

```text
access -> usecase
usecase -> contracts and usecase-defined ports
infra -> pkg/clusterv2 and pkg/channelv2, implementing usecase ports
app -> access, usecase, infra, log, pkg composition dependencies
```

`internalv2/usecase/message` must remain protocol- and cluster-agnostic. It
must not import `pkg/gateway`, `pkg/protocol/frame`, `pkg/clusterv2`,
`pkg/channelv2`, `internalv2/access`, or `internalv2/app`.

## Phase-1 Send Flow

```text
pkg/gateway SendPacket
  -> internalv2/access/gateway.Handler
  -> internalv2/usecase/message.App thin facade
  -> internalv2/runtime/channelappend.Router resolves channel append authority
  -> local channelappend.Group append authority or access/node Channel Append RPC
  -> authority writer validates, assigns message IDs, and appends through infra/cluster.ChannelAppender
  -> pkg/clusterv2.Node.AppendChannelBatch -> pkg/channelv2 append
  -> internalv2/usecase/message.SendResult
  -> internalv2/access/gateway writes SendackPacket
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
  -> internalv2/access/api conversation routes
  -> internalv2/usecase/conversation
  -> internalv2/infra/cluster ConversationStore
  -> ListConversationActiveView(ConversationKindNormal, uid)
  -> read latest non-CMD ChannelV2 messages for visible rows

CMD offline sync/syncack
  -> internalv2/access/api /message/sync or /message/syncack
  -> internalv2/usecase/cmdsync
  -> internalv2/infra/cluster CMDSyncStore
  -> ListConversationActivePage(ConversationKindCMD, uid)
  -> read only SyncOnce or command-channel ChannelV2 messages
  -> syncack writes ConversationKindCMD read cursors
```

`pkg/db/meta` owns one canonical conversation projection table keyed by
`(uid, kind, channel_id, channel_type)`. Both ordinary and CMD rows are routed
by the UID hash slot, including single-node cluster deployments. Ordinary
conversation storage and listing do not infer semantics from the `____cmd`
suffix; the suffix remains a legacy command-channel naming detail, while
ordinary/CMD separation is carried by explicit `ConversationKind` and the
durable ChannelV2 `SyncOnce` marker.

## Phase-1 Presence Flow

```text
pkg/gateway CONNECT activation
  -> internalv2/access/gateway.Handler
  -> internalv2/usecase/presence.App.Activate
  -> internalv2/runtime/online pending route
  -> internalv2/infra/cluster.PresenceAuthorityClient
  -> local runtime/presence.Directory or access/node RPC to the current authority
  -> internalv2/runtime/online active route
```

Route-authority changes are observed from `pkg/clusterv2`. When this node gains
authority for a hash slot, `internalv2/app` installs the corresponding
`runtime/presence.Directory` authority epoch. Owner gateway PING marks active
sessions dirty in `runtime/online`; the app worker batches those dirty routes,
resolves their current UID authorities, sends `TouchRoutes`, and expires
authority routes by TTL. When leadership moves elsewhere, local authority state
for that hash slot is cleared. Authority changes do not scan or replay all
owner-local active sessions.

## Phase-1 Bench Target Flow

```text
wkbench target preflight
  -> internalv2/access/api healthz, readyz, bench/v1 capabilities
wkbench capacity discovery
  -> internalv2/access/api bench/v1 capacity-target
wkbench prepare
  -> internalv2/access/api benchmark-only setup acknowledgments
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
  -> internalv2/app top collector
  -> internalv2/access/api /top/v1/snapshot
  -> wkcli top client-side multi-node aggregation
```

## Diagnostics Trace Flow

```text
manager diagnostics HTTP request
  -> internalv2/access/manager request validation and permissions
  -> internalv2/usecase/management diagnostics orchestration
  -> local internalv2/observability/diagnostics store or access/node Manager Diagnostics RPC
  -> bounded trace/message/event result page or tracking-rule mutation result
```

Diagnostics trace storage and tracking rules belong to `internalv2`; new v2
manager routes must not import the legacy `internal/observability/diagnostics`
package.

## Legacy Channel Management Flow

```text
legacy /channel* HTTP request
  -> internalv2/access/api request validation and legacy JSON envelope
  -> internalv2/usecase/channel
  -> internalv2/infra/cluster ChannelMetadataStore
  -> pkg/clusterv2.Node Slot metadata facade
  -> Slot Raft propose for mutations or routed Slot metadata read for list/get
```

Temporary subscribers, allowlists, and denylists use stable internal member-list
channel IDs so data remains compatible with the legacy metadata layout. These
APIs do not bypass cluster semantics; single-node deployment is handled as a
single-node cluster.

## Promoted Entrypoint Boundary

- `cmd/wukongim` is the promoted product entrypoint and wires
  `internalv2/app` by default.
- Plugin host RPC protobuf wire contracts live in `pkg/plugin/pluginproto`;
  keep field numbers compatible with `github.com/WuKongIM/go-pdk`.
- Do not rename `internalv2`, `pkg/clusterv2`, `pkg/controllerv2`, or
  `pkg/channelv2` package paths in this promotion stage.
- Do not migrate legacy plugin hooks or remaining management APIs not listed in
  the internalv2 manager/access flows.
- Do not implement realtime `NoPersist` delivery yet; return a stable
  unsupported result until that runtime exists.
- Do not advertise legacy message fields that `channelv2.Message` cannot
  persist or replicate today.
