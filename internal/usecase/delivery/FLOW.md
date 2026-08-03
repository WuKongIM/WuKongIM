# internal/usecase/delivery Flow

## Responsibility

`internal/usecase/delivery` is the temporary entry-agnostic gateway feedback
facade for receive-ack and session-close commands.

Production app composition now uses this package only as the temporary gateway
feedback facade. Channelappend submits canonical recipient plans directly to
`internal/contracts/onlinedelivery`. `SubmitCommitted` remains temporarily as
an explicit compatibility rejection surface for callers compiled against the
old usecase API; production composition returns the canonical-plan-required
error and never converts the event into fanout work.

The package must not import gateway frames, access adapters, app composition,
or concrete cluster/runtime implementations. Runtime adapters are responsible
for bridging these usecase DTOs to concrete runtime DTOs.

## Flow

```text
MessageCommitted compatibility call
  -> App.SubmitCommitted
  -> production runtime adapter rejects: canonical recipient plan required

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
- `pkg/channel`
- `internal/access`
- `internal/app`
- `internal/runtime/delivery`
