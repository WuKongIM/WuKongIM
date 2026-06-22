# internalv2 Plugin Benchmark Baseline Design

## Scope

Add focused benchmark coverage for the plugin migration paths that are still
easy to regress during the next migration steps.

This pass is intentionally narrow. It does not optimize code, change runtime
behavior, add metrics, or run large black-box `wkbench` scenarios.

## Context

The current internalv2 plugin benchmark coverage already includes:

- send hook candidate selection and `BeforeSend`
- PersistAfter event cloning and message-batch mapping
- plugin host RPC mappings for message, channel messages, cluster, conversation,
  and HTTP forward local/remote calls
- plugin hook worker enqueue pressure
- channelappend durable submit, post-commit plugin enqueue, and recipient
  delivery worker enqueue

Two high-risk migration edges are not directly pinned:

- command-style `NoPersist` realtime dispatch through the real local
  channelappend submit path
- the constant-time `/plugin/httpForward toNodeId=-1` fanout-deferred branch

## Decision

Add two microbenchmark baselines:

1. `BenchmarkSubmitLocalNoPersistRealtimeScoped` in
   `internalv2/runtime/channelappend`.
2. `BenchmarkHTTPForwardFanoutDeferred` in `internalv2/usecase/plugin`.

The NoPersist benchmark should use `Group.SubmitLocal` with a configured
recipient delivery enqueuer so it measures the production local-authority
realtime path, not just the pure `realtimeEffect` helper.

The fanout benchmark should assert the typed deferred error and no remote/local
forward side effect. This keeps the compatibility decision measurable as a
cheap branch before future cluster-wide plugin work.

## Data Flow

```text
command-style NoPersist SEND
  -> Group.SubmitLocal
  -> prepareNoPersistRealtimeSend
  -> realtimeEffect
  -> dispatchCommittedRecipientsForTarget
  -> RecipientDeliveryEnqueuer.EnqueueRecipientBatch
  -> SENDACK success
```

```text
plugin /plugin/httpForward toNodeId=-1
  -> App.HTTPForward
  -> ErrHTTPForwardFanoutDeferred
```

## Testing

Run focused unit tests and benchmark smoke commands:

```bash
go test ./internalv2/runtime/channelappend ./internalv2/usecase/plugin -run '^$' -bench 'Benchmark(SubmitLocalNoPersistRealtimeScoped|HTTPForwardFanoutDeferred)$' -benchtime=1x
go test ./internalv2/runtime/channelappend ./internalv2/usecase/plugin -count=1
```

The benchmark pass must compile, execute both benchmark names, and report
allocations through `b.ReportAllocs()`.

## Non-Goals

- No production optimization in this pass
- No benchmark threshold enforcement
- No e2e or integration benchmark
- No `/plugin/httpForward` fanout implementation
- No plugin PDK or protobuf changes
