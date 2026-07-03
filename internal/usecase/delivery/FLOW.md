# internal/usecase/delivery Flow

## Responsibility

`internal/usecase/delivery` owns the entry-agnostic online delivery
orchestration boundary. It accepts committed-message events from message
orchestration and forwards receive-ack and session-close feedback to the
configured runtime port.

The package must not import gateway frames, access adapters, app composition,
or concrete cluster/runtime implementations. Runtime adapters are responsible
for bridging these usecase DTOs to concrete runtime DTOs.

## Flow

```text
MessageCommitted
  -> App.SubmitCommitted
  -> runtime.SubmitCommitted

RecvackCommand
  -> App.Recvack
  -> runtime.Recvack

SessionClosedCommand
  -> App.SessionClosed
  -> runtime.SessionClosed
```

## Import Boundary

The usecase package must remain independent from concrete entries and cluster
adapters. The import-boundary test rejects imports of:

- `pkg/gateway`
- `pkg/protocol/frame`
- `pkg/cluster`
- `pkg/channelv2`
- `internal/access`
- `internal/app`
- `internal/runtime/delivery`
