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
| `access/gateway` | Gateway frame adapter: `SendPacket` mapping, sendack writing, and entry error mapping. |
| `usecase/message` | Entry-agnostic SEND orchestration, batching, validation, message ID allocation, append ports, and committed event submission. |
| `infra/cluster` | Adapter from message append ports to `pkg/clusterv2` / `pkg/channelv2`. |
| `contracts/messageevents` | Lightweight committed-message event DTOs for later delivery/conversation migration. |

## Dependency Direction

```text
access -> usecase
usecase -> contracts and usecase-defined ports
infra -> pkg/clusterv2 and pkg/channelv2, implementing usecase ports
app -> access, usecase, infra, pkg composition dependencies
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
