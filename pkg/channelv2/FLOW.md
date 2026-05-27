# pkg/channelv2 Flow

## Directory Tree

```text
pkg/channelv2/        - Experimental multi-reactor channel log runtime; root DTOs, errors, Cluster facade, Config, tests, and benchmarks.
|-- machine/          - Pure per-channel state transitions for metadata, append, progress, and invariants; no blocking I/O.
|-- reactor/          - Channel-key ownership, priority mailboxes, append queues, scheduler, lifecycle, metrics, and worker-result application.
|-- replication/      - Leader/follower replication helpers and protocol decisions used by reactor runtime paths.
|-- service/          - Synchronous facade that validates requests, requires preloaded append state, lazily activates PullHint followers, routes work to reactors, and waits on futures.
|-- store/            - Narrow persistence contract, memory store, and `pkg/db/message` compatibility adapter boundary.
|-- testkit/          - In-memory multi-node cluster harness for channelv2 tests.
|-- transport/        - V0 local/RPC transport DTOs for pull, ack, notify compatibility, and PullHint.
`-- worker/           - Typed bounded worker pools for store append/read/apply, RPC pull/ack/PullHint, checkpoint, and result delivery.
```

`store/channel_adapter.go` is the only channelv2 file that may import `pkg/channel` DTOs required by the `pkg/db/message` engine; other channelv2 packages should depend on channelv2 interfaces.

Diagram labels use `event or guard / effect` so agents can distinguish triggers from side effects.

## Append Sequence

```mermaid
sequenceDiagram
    participant Caller
    participant Service as service facade
    participant Reactor as owning reactor
    participant Workers as typed worker pools
    participant Follower as follower reactor

    Caller->>Service: Append / AppendBatch
    Service->>Reactor: ReserveAppend(key) and HasChannelState(key)
    alt channel runtime not loaded
        Service-->>Caller: ErrChannelNotFound
    end
    Service->>Reactor: submit append event
    Reactor->>Reactor: validate leader, epoch, capacity
    alt not leader, stale meta, or queue full
        Reactor-->>Service: complete future with typed error
        Service-->>Caller: error
    else accepted
        Reactor->>Reactor: enqueue per-channel append queue
        Reactor->>Reactor: flush by max records, bytes, or wait
        Reactor->>Workers: TaskStoreAppend(batch, fence)
        Workers-->>Reactor: append result
        Reactor->>Reactor: apply fenced result and update local progress

        alt CommitModeLocal
            Reactor-->>Service: complete future after local durable append
            Service-->>Caller: AppendResult / AppendBatchResult
        else CommitModeQuorum
            Reactor->>Workers: TaskRPCPullHint(lagging followers)
            Workers-->>Follower: PullHint
            loop until leader HW covers appended records
                Follower->>Workers: TaskRPCPull(leader, local LEO + 1)
                Workers->>Reactor: EventPull
                alt requested offsets covered by leader recent-record cache
                    Reactor-->>Follower: PullResponse(records, leader HW, leader LEO)
                else cache miss or older prefix needed
                    Reactor->>Workers: TaskStoreReadLog
                    Workers-->>Reactor: store prefix records
                    Reactor-->>Follower: PullResponse(store prefix + optional cache suffix, leader HW, leader LEO)
                end
                Follower->>Workers: TaskStoreApply(records)
                Workers-->>Follower: apply result
                Follower->>Workers: TaskRPCAck(match offset)
                Workers->>Reactor: EventAck
                Reactor->>Reactor: AdvanceHW
            end
            Reactor-->>Service: complete quorum future
            Service-->>Caller: AppendResult / AppendBatchResult
        end
    end
```

Leader reactors keep a small configurable recent-record suffix cache for durable append records. Follower `Pull` requests that are covered by this suffix can complete from memory; older requests still use `TaskStoreReadLog`, and the leader may append a cache-covered suffix to the store prefix when doing so does not create gaps. The cache is cleared by metadata fences or role changes and is only a performance optimization.

## Channel Runtime Lifecycle Model

`Unloaded` is represented by absence from the owning reactor's `channels` map.
Loaded runtimes hold `machine.ChannelState`, `appendQueue`, `replicationState`,
leader-visible follower state, and an explicit `runtimeLifecycle` phase model.
Metadata reload is not a long-lived phase: accepted metadata fence changes fail
stale waiters, reset transient lifecycle/replication state, apply the new
`Meta`, and then choose the leader or follower runtime path from local role.

Leader phases:

- `Serving`: normal hot or idle leader runtime. Idle slowdown is derived from
  idle age and `leaderPullDelay`; it is not stored as a lifecycle phase.
- `StoppingFollowers`: the leader is idle-expired and waits for caught-up
  followers to pull at `LEO+1` so it can return `PullControlStop`.
- `Checkpointing`: all followers stopped for the current activity version and
  the leader checkpoint is in flight or retrying.
- `FinalRecheck`: the checkpoint finished and a normal-priority recheck fences
  leader eviction behind same-channel append reservations and submit sequence
  changes.

Follower phases:

- `Replicating`: ordinary pull, apply, ACK, park, and retry behavior.
- `StopCheckpointing`: the follower accepted `PullControlStop` after local
  LEO/HW caught up and is checkpointing before the stopped ACK.
- `StopAcking`: the checkpoint succeeded and the follower is sending or
  retrying the stopped ACK before unloading runtime state.

```mermaid
stateDiagram-v2
    [*] --> Unloaded
    Unloaded --> Loaded: ApplyMeta or PullHint lazy activation / load store state
    Loaded --> LeaderServing: local role = leader
    Loaded --> FollowerReplicating: local role = follower

    LeaderServing --> LeaderStoppingFollowers: idle expired && HW == LEO && followers caught up
    LeaderStoppingFollowers --> LeaderServing: append or metadata fence
    LeaderStoppingFollowers --> LeaderCheckpointing: all followers stopped at ActivityVersion
    LeaderCheckpointing --> LeaderServing: append or metadata fence
    LeaderCheckpointing --> LeaderFinalRecheck: checkpoint done and guards still pass
    LeaderFinalRecheck --> LeaderServing: append reservation or submit sequence change
    LeaderFinalRecheck --> Unloaded: no pending work / evict runtime

    FollowerReplicating --> FollowerStopCheckpointing: PullControlStop && local LEO/HW caught up
    FollowerStopCheckpointing --> FollowerReplicating: newer PullHint or metadata fence
    FollowerStopCheckpointing --> FollowerStopAcking: checkpoint done
    FollowerStopAcking --> FollowerReplicating: stale stopped ACK metadata
    FollowerStopAcking --> Unloaded: stopped ACK succeeds / evict runtime

    LeaderServing --> Unloaded: Close
    FollowerReplicating --> Unloaded: Close
```

Lifecycle decisions are expressed as reactor-owned actions such as starting a
checkpoint, scheduling lifecycle retry, queuing leader final recheck, sending a
stopped ACK, or evicting runtime. Store and transport I/O still run through the
existing worker pools; the lifecycle model only decides what should happen next.
