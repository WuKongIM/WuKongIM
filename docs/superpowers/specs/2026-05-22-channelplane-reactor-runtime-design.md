# Channelplane Reactor Runtime Design

## Goal

Upgrade `internal/runtime/channelplane` from a reactor-shaped synchronous router into a bounded reactor runtime for durable channel appends. The new runtime must preserve same-channel ordering, centralize route refresh/retry/backpressure, and make peer batching resilient to slow remote RPCs.

## Constraints

- Keep external `Plane.AppendBatch(ctx, req)` synchronous and API-compatible.
- Preserve the project boundary: `channelplane` may depend on `pkg/channel`, `internal/runtime/channelmeta` ports, metrics/observability contracts, and neutral peer DTOs only.
- Do not move message auth, hooks, sendack, or committed side effects into `channelplane`.
- Do not add deployment semantics that bypass cluster routing; a single node remains a single-node cluster.
- Prefer internal defaults for new runtime budgets unless an existing config value already fits.

## Architecture

`Plane` remains the facade. It hashes each append to one reactor shard and waits on a future. Each `reactor` owns all `channelCell` state for its shard and is the only goroutine allowed to mutate cell state. A real `scheduler` tracks ready channel keys and advances them round-robin so one hot channel cannot monopolize a shard.

Potentially blocking work runs outside the reactor through bounded effect executors. A cell emits one action at a time: resolve route, append local, enqueue remote append, or complete terminally. The effect returns a completion event to the owning reactor. This preserves strict same-channel serialization while bounding goroutine growth.

`PeerReactor` batches remote owner appends by target node and lane. Lane loops collect tasks and build batches, but remote `AppendBatches` RPCs run in bounded RPC workers with explicit timeouts. A slow peer consumes peer inflight budget and eventually backpressures that peer/lane without blocking the lane event loop from accepting, timing, or failing other tasks.

## State Model

Each `channelCell` keeps:

- `pending`: bounded FIFO append commands.
- `route`: current route projection and epoch.
- `inflight`: the command currently owning the channel effect.
- `state`: idle, resolving route, appending local, appending remote, closing.
- `retry`: per-command route invalidation retry budget.

The cell starts at most one effect. Completion either retries after invalidating the route, completes the future, or schedules the next pending command.

## Error Semantics

Route invalidation errors are `ErrStaleRoute`, `channel.ErrStaleMeta`, `channel.ErrNotLeader`, `channel.ErrLeaseExpired`, and `channel.ErrWriteFenced`. A command gets one immediate authoritative refresh retry. If the fresh attempt still fails, the typed error is returned.

Backpressure errors remain typed: `ErrOverloaded` for reactor/cell/executor pressure and `ErrPeerBackpressured` for peer lane pressure. Invalid requests remain terminal.

## Shutdown

Shutdown has explicit gates:

1. Plane stops accepting new appends.
2. Reactors and lanes reject new submissions.
3. Queued commands are drained and completed with `ErrClosed`.
4. Effect executors and peer RPC workers are cancelled.
5. Inflight commands are completed exactly once.
6. Reactor, peer lane, and executor goroutines exit.

No append accepted before or during shutdown may be left without terminal future completion.

## Testing Strategy

Tests focus on observable runtime guarantees:

- Same-channel ordering stays strict.
- Scheduler gives independent channels progress under a hot channel.
- Stop/submit races never hang a future.
- Route invalidation includes write-fenced local and remote results.
- Resolver invalidation does not join or cache stale in-flight lookups.
- Effect executor enforces bounded inflight behavior.
- Peer lanes keep accepting/failing tasks while a previous RPC is blocked.
- Peer RPC timeout releases pending budget.
- Observer queued/completed events are balanced for accepted commands.

