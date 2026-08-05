# internal Flow

## Responsibility

`internal` is the promoted business kernel for the new architecture. It owns
the product entry adapters, entry-agnostic usecases, node-local runtimes,
infrastructure adapters, and the single composition root used by
`cmd/wukongim`. The former v1 server runtime has been removed; new internal
work must stay on the promoted access/usecase/runtime/infra/app boundaries.

The promoted runtime proves the client `SEND -> SENDACK` write path through
`pkg/cluster` and `pkg/channel`. It also exposes legacy-compatible channel,
user, message, membership-backed conversation, and CMD sync HTTP surfaces backed by cluster Slot
metadata and Channel runtime logs.

Single-node deployment is still a single-node cluster. Do not add send,
storage, or routing branches that bypass cluster semantics.

## Package Boundaries

| Package | Responsibility |
|---------|----------------|
| `app` | Single composition root for config, dependency wiring, and lifecycle. |
| `access/api` | Health, readiness, bench/v1 target HTTP surface, legacy `/route` address lookup, and legacy-compatible channel/user/message/conversation/CMD sync HTTP adapters. |
| `access/gateway` | Gateway event/frame adapter: presence activation/deactivation mapping, `SendPacket` mapping, sendack writing, and entry error mapping. |
| `access/manager` | Manager HTTP adapter for diagnostics, management views, and authenticated backup/restore operations. |
| `access/node` | Node RPC adapter for presence, delivery, channel append, scheduled backup, and staged restore calls between internal nodes. |
| `log` | Zap/lumberjack-backed application logger for the internal composition root. |
| `observability/diagnostics` | Bounded node-local diagnostics events, trace indexing, runtime tracking rules, and sendtrace context helpers. |
| `usecase/channel` | Entry-agnostic channel metadata, subscriber, temporary subscriber, allowlist, and denylist orchestration. |
| `usecase/cmdsync` | Entry-agnostic CMD binding, durable offline sync, and syncack over separate UID-owned CMD memberships and CMD logs. |
| `usecase/conversation` | Entry-agnostic transient conversation construction and badge/hide/activation orchestration over ordinary UID-owned memberships. |
| `usecase/delivery` | Temporary entry-agnostic gateway RECVACK/session-close feedback facade plus explicit rejection of old committed-event submissions. |
| `usecase/management` | Entry-agnostic management read orchestration for manager adapters. |
| `usecase/message` | Entry-agnostic SEND facade and compatible channel message sync. |
| `usecase/presence` | Entry-agnostic connection presence activation, deactivation, lookup, and authority coordination. |
| `usecase/user` | Entry-agnostic user token, device quit, online status, and system UID compatibility orchestration. |
| `usecase/backup` | Single-plan scheduled full-backup admission, archive management, and resumable maintenance restore orchestration. |
| `runtime/delivery` | Canonical node-local recipient-plan execution, owner push, bounded exact-route retry, and RECVACK state. |
| `runtime/online` | Owner-local active gateway session registry used for local delivery and dirty touch batching. |
| `runtime/presence` | In-memory UID route authority directory for hash slots locally led by this node. |
| `runtime/channelappend` | Channel-authority write group where each local authoritative channel is served by an independent single-writer state machine, hash-sharded for lookup and advanced by shared worker pools. |
| `runtime/backup` | Leader-only schedule evaluation plus portable full-archive stream publication. |
| `infra/cluster` | Adapter from channel append, channel/user metadata, presence, conversation, and CMD sync ports to `pkg/cluster` / `pkg/channel`. |
| `infra/backup` | File/OSS/COS/S3-compatible repository adapters, cluster export coordination, archive finalization, and crash-safe node-local staged restore. |
| `contracts/backup` | Bounded Controller/RPC DTOs for one scheduled full-backup subsystem. |
| `contracts/channelmembers` | Stable legacy-compatible member-list channel-id namespace helpers. |
| `contracts/messageevents` | Lightweight committed-message event DTOs for delivery and event projection. |

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
that channel. An ordinary durable append writes the Channel log and its
sender-sequence index atomically, then schedules online delivery and other
independent post-commit effects. SEND never writes recipient memberships or a
conversation projection. CMD and `SyncOnce` sends use separate CMD Channel
logs and likewise do not mutate UID directory state.

## Membership-Backed Conversation Flow

```text
ordinary conversation list
  -> internal/access/api conversation routes
  -> internal/usecase/conversation
  -> internal/infra/cluster ConversationStore
  -> page one UID's user_channel_membership activation index
  -> group live candidates by exact Channel Leader
  -> batch-read committed head, retention floor, last display message,
     and current-user sender sequence
  -> construct transient conversations; return deletes and unresolved keys

CMD bind/sync/syncack
  -> internal/access/api /message/sync or /message/syncack
  -> internal/usecase/cmdsync
  -> internal/infra/cluster CMDSyncStore
  -> page user_cmd_channel_membership for the UID
  -> read only the separately sequenced CMD Channel logs
  -> syncack advances membership ack_seq
```

`pkg/db/meta` owns separate `user_channel_membership` and
`user_cmd_channel_membership` tables, both routed by UID hash slot. There is no
durable conversation table or conversation-active runtime. Ordinary
`activated_at` changes only on explicit navigation or hide; message SEND,
delivery, and pull leave both membership tables unchanged.

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
