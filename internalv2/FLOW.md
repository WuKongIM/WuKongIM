# internalv2 Flow

## Responsibility

`internalv2` is a parallel business kernel for the new architecture. Phase 1
keeps the existing `internal` production path unchanged and proves the client
`SEND -> SENDACK` write skeleton through `pkg/clusterv2` and `pkg/channelv2`.
It also exposes a legacy-compatible channel management HTTP surface backed by
the clusterv2 Slot metadata path.

Single-node deployment is still a single-node cluster. Do not add send,
storage, or routing branches that bypass cluster semantics.

## Package Boundaries

| Package | Responsibility |
|---------|----------------|
| `app` | Single composition root for config, dependency wiring, and lifecycle. |
| `access/api` | Health, readiness, bench/v1 target HTTP surface, and legacy-compatible channel management HTTP adapters. |
| `access/gateway` | Gateway event/frame adapter: presence activation/deactivation mapping, `SendPacket` mapping, sendack writing, and entry error mapping. |
| `access/node` | Node RPC adapter for presence, conversation authority, delivery, and channel write calls between internalv2 nodes. |
| `log` | Zap/lumberjack-backed application logger for the internalv2 composition root. |
| `usecase/channel` | Entry-agnostic channel metadata, subscriber, temporary subscriber, allowlist, and denylist orchestration. |
| `usecase/message` | Entry-agnostic SEND facade and compatible channel message sync. |
| `usecase/presence` | Entry-agnostic connection presence activation, deactivation, lookup, and authority coordination. |
| `runtime/online` | Owner-local active gateway session registry used for local delivery and dirty touch batching. |
| `runtime/presence` | In-memory UID route authority directory for hash slots locally led by this node. |
| `runtime/channelwrite` | Channel-authority write reactors that own SEND validation, message ID allocation, append admission, recipient grouping, conversation projection, and optional delivery effects. |
| `infra/cluster` | Adapter from channel write, channel metadata, delivery, presence, and conversation ports to `pkg/clusterv2` / `pkg/channelv2`. |
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
  -> internalv2/runtime/channelwrite.Router resolves channel append authority
  -> local channelwrite.Group authority reactor or access/node Channel Write RPC
  -> authority reactor validates, assigns message IDs, and appends through infra/cluster.ChannelAppender
  -> pkg/clusterv2.Node.AppendChannelBatch -> pkg/channelv2 append
  -> internalv2/usecase/message.SendResult
  -> internalv2/access/gateway writes SendackPacket
```

Only the channel authority node creates and owns real channel write state. A
non-authority node forwards the batch to the authority node through Channel
Write RPC and does not create proxy channel state or enter a local channel
reactor for that channel. Conversation projection, recipient authority grouping,
owner push, and delivery fanout run after the successful append in the
authority reactor's best-effort post-commit pipeline.

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
path so gateway micro-batching and future send reactors do not grow separate
behavior.

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

## Phase-1 Non-Goals

- Do not wire `internalv2` into `cmd/wukongim` yet.
- Do not migrate legacy message sync, conversation, CMD, plugin hooks, or
  management APIs.
- Do not implement realtime `NoPersist` delivery yet; return a stable
  unsupported result until that runtime exists.
- Do not advertise legacy message fields that `channelv2.Message` cannot
  persist or replicate today.
