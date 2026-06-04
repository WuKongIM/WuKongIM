# internalv2 Sendtrace Migration Design

## Context

Legacy `internal` records message SEND latency facts through
`pkg/observability/sendtrace` and adapts those events into the node-local
diagnostics store in `internal/observability/diagnostics`. The store is bounded,
sampled, and in-memory. It keeps successful events at a low sample rate, keeps
slow and error events by policy, and restores the global sendtrace sink during
app shutdown.

`internalv2` already has clear entry/usecase/infra/app boundaries:

- `internalv2/access/gateway` maps gateway frames and writes SENDACK.
- `internalv2/usecase/message` owns entry-agnostic SEND orchestration.
- `internalv2/infra/cluster` adapts message append ports to `pkg/clusterv2` and
  `pkg/channelv2`.
- `internalv2/app` is the only composition root.

The first migration slice should make internalv2 SEND traces available to tests
and local app state. It should not add HTTP debug query APIs or manager
surfaces.

## Goals

- Wire an internalv2 node-local diagnostics store and sampler behind
  `pkg/observability/sendtrace`.
- Propagate a diagnostics trace id through gateway SEND, message usecase append,
  and clusterv2/channelv2 append requests.
- Record the first internalv2 SEND stages needed for latency triage:
  gateway send, gateway SENDACK write, message durable append, and channel append
  adapter completion.
- Preserve internalv2 layering. Gateway protocol details stay in
  `access/gateway`; cluster types stay in `infra/cluster`; app owns sink wiring.
- Keep hot-path overhead bounded and explicit when diagnostics are disabled.

## Non-Goals

- No HTTP or manager diagnostics query API in this slice.
- No migration of legacy diagnostics aggregation across nodes.
- No broad refactor of the legacy diagnostics package.
- No attempt to make every ChannelV2 reactor and storage sub-stage
  trace-correlated in the first pass.

## Architecture

Reuse the existing packages:

- `pkg/observability/sendtrace` remains the global event interface.
- `internal/observability/diagnostics` remains the bounded store and sampler
  implementation for this first migration slice.

`internalv2/app.Config.Observability` will gain a `DiagnosticsConfig` matching
the legacy knobs that matter for capture:

- `Enabled`
- `BufferSize`
- `SampleRate`
- `SlowThreshold`
- `ErrorSampleRate`
- `DebugMatches`

Defaults should mirror the legacy app unless the v2 standalone config already
sets a stronger convention: diagnostics enabled by default, buffer size 50000,
successful sample rate 0.01, slow threshold 500ms, error sample rate 1.0.

`internalv2/app.New` will create the diagnostics store and tracking rules only
when diagnostics are enabled. It will install a sendtrace sink with
`sendtrace.SetSink` and store the restore callback on `App`. `Stop` will restore
the previous sink before returning. Construction failure after installing the
sink must also restore it.

## Data Flow

### Gateway SEND

When diagnostics are enabled, `internalv2/access/gateway` will ensure a trace id
for each valid SEND and attach it to `message.SendCommand.TraceID`. It will also
compute a diagnostics-safe channel key and record `gateway.messages_send` around
the call into the message usecase.

When diagnostics are disabled, gateway should avoid trace id generation and
channel key derivation. This keeps the disabled path close to a single
`sendtrace.Enabled` check at the entry boundary.

### Gateway SENDACK

After `writeSendack` succeeds, gateway will record `gateway.write_sendack` with
the same trace id, client message number, channel key, sender UID, and result
message sequence. This must remain separate from the low-cardinality
`SendackObserver` used for metrics.

### Message Durable Append

`internalv2/usecase/message.SendCommand` will add `TraceID`. The message usecase
will record `message.send_durable` for each active item after the append result
is known. Batch-level append errors should emit one event per active item with a
classified non-ok result. Successful item results should include `MessageSeq`.

`AppendBatchRequest` will add `TraceID` and `Attempt`. The first pass can use
the first active item trace id for a channel segment, matching the current
legacy behavior for grouped sends.

### Cluster/Channel Append Adapter

`internalv2/infra/cluster.ChannelAppender` will forward trace metadata into
`pkg/channelv2.AppendBatchRequest` once that request type gains matching fields.
It will record `channel.append.local` from the adapter boundary with batch result
count, per-item sequence, and append duration. The stage name intentionally
matches the legacy trace taxonomy so diagnostics consumers can compare old and
new SEND paths.

Further ChannelV2 reactor sub-stage correlation can be added later through
trace-aware request metadata and observer events. This keeps this slice focused
while still making the SEND path queryable end to end at the app/usecase
boundary.

## Performance

The disabled path should not generate trace ids or channel keys at gateway
entry. Downstream unconditional `sendtrace.Record` calls still do a small amount
of work to construct events and compute durations, then return after the global
sink check.

The enabled path relies on the existing sampler before taking the store lock.
Ordinary successful events are sampled; slow and error events are retained by
policy. Runtime debug/tracking rules should remain bounded and TTL based.

Avoid adding high-cardinality fields to Prometheus metrics observers. Trace ids,
client message numbers, channel keys, and UIDs should stay in sendtrace events
and diagnostics storage only.

## Testing

Add focused tests before implementation:

- App wiring installs a diagnostics sendtrace sink when enabled and restores it
  on stop.
- Gateway assigns trace ids only when sendtrace is enabled and records gateway
  send/SENDACK stages.
- Message usecase records `message.send_durable` for successful and failed
  append paths, with client message number, channel key, sender UID, and
  sequence when available.
- Infra appender forwards trace metadata to channelv2 append requests.
- Import boundary tests continue passing for message usecase and gateway.

Run targeted unit tests for the touched packages:

```bash
go test ./internalv2/access/gateway ./internalv2/usecase/message ./internalv2/infra/cluster ./internalv2/app ./pkg/channelv2 ./pkg/observability/sendtrace ./internal/observability/diagnostics
```

## Rollout

This is internalv2-only and does not affect the legacy `cmd/wukongim` runtime.
The standalone `cmd/wukongimv2` config should expose the diagnostics knobs and
its example config files should be updated when the config fields are added.
