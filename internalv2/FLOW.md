# internalv2 Flow

## Responsibility

`internalv2` is a parallel business kernel for the new architecture. Phase 1
keeps the existing `internal` production path unchanged and proves the client
`SEND -> SENDACK` write skeleton through `pkg/clusterv2` and `pkg/channelv2`.

Single-node deployment is still a single-node cluster. Do not add send,
storage, or routing branches that bypass cluster semantics.

## Package Boundaries

| Package | Responsibility |
|---------|----------------|
| `app` | Single composition root for config, dependency wiring, and lifecycle. |
| `access/api` | Minimal health, readiness, and bench/v1 target HTTP surface for phase-1 SEND -> SENDACK benchmarking. |
| `access/gateway` | Gateway event/frame adapter: presence activation/deactivation mapping, `SendPacket` mapping, sendack writing, and entry error mapping. |
| `access/node` | Node RPC adapter for presence authority calls between internalv2 nodes. |
| `log` | Zap/lumberjack-backed application logger for the internalv2 composition root. |
| `usecase/message` | Entry-agnostic SEND orchestration, batching, validation, message ID allocation, append ports, and committed event submission. |
| `usecase/presence` | Entry-agnostic connection presence activation, deactivation, lookup, and authority coordination. |
| `runtime/online` | Owner-local active gateway session registry used for local delivery and dirty touch batching. |
| `runtime/presence` | In-memory UID route authority directory for hash slots locally led by this node. |
| `infra/cluster` | Adapter from message append ports and presence authority ports to `pkg/clusterv2` / `pkg/channelv2`. |
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
  -> internalv2/usecase/message.App.SendBatch
  -> internalv2/infra/cluster.ChannelAppender
  -> pkg/clusterv2.Node.AppendChannelBatch
  -> pkg/channelv2 append
  -> internalv2/usecase/message.SendResult
  -> internalv2/access/gateway writes SendackPacket
```

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

## Phase-1 Non-Goals

- Do not wire `internalv2` into `cmd/wukongim` yet.
- Do not migrate delivery, conversation, CMD, plugin hooks, or management APIs.
- Do not implement realtime `NoPersist` delivery yet; return a stable
  unsupported result until that runtime exists.
- Do not advertise legacy message fields that `channelv2.Message` cannot
  persist or replicate today.
