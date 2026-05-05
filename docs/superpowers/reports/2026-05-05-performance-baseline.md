# Performance Baseline - 2026-05-05

## Environment

```text
go version go1.23.4 darwin/arm64

GOOGLE_GENAI_USE_GCA=true
GOPATH=/Users/tt/go
GOMAXPROCS=

?? docs/superpowers/plans/2026-05-05-performance-optimization.md
?? docs/superpowers/reports/2026-05-05-performance-baseline.md
```

## Gateway

Command: `GOWORK=off go test -run '^$' -bench 'BenchmarkServer(OpenIdleSessionBatch|SendDispatch)' -benchmem ./internal/gateway/core`

```text
BenchmarkServerOpenIdleSessionBatch/idle_monitor_disabled-10     1832420 ns/op  3949378 B/op  24907 allocs/op
BenchmarkServerOpenIdleSessionBatch/idle_monitor_enabled-10      1888126 ns/op  3949552 B/op  24910 allocs/op
BenchmarkServerSendDispatch/sync-10                                  270.0 ns/op      241 B/op      4 allocs/op
BenchmarkServerSendDispatch/async-10                                 520.0 ns/op      529 B/op      6 allocs/op
PASS
```

## Delivery

Command: `GOWORK=off go test -run '^$' -bench . -benchmem ./internal/runtime/delivery ./internal/app`

```text
BenchmarkRetryWheelSchedule-10                                   269.6 ns/op      616 B/op      0 allocs/op
BenchmarkBuildRealtimeRecvPacketPersonChannelView-10              97.64 ns/op     288 B/op      3 allocs/op
BenchmarkLocalDeliveryPushPersonRoutes-10                      11519 ns/op     24800 B/op     15 allocs/op
BenchmarkDistributedDeliveryPushGroupBatchRoutes-10             29055 ns/op     95699 B/op     29 allocs/op
BenchmarkDistributedDeliveryPushPersonRouteViews-10             44017 ns/op    128345 B/op    317 allocs/op
BenchmarkAsyncCommittedDispatcherSubmitCommitted-10               207.1 ns/op     119 B/op      4 allocs/op
BenchmarkLocalDeliveryResolverResolvePagePersonChannel-10         221.0 ns/op     272 B/op      5 allocs/op
PASS
```

## Channel Store

Command: `GOWORK=off go test -run '^$' -bench . -benchmem ./pkg/channel/store ./pkg/transport ./pkg/slot/multiraft`

```text
pkg/channel/store: PASS, no benchmark output in package.
```

## Multi-Raft / Transport

Command: `GOWORK=off go test -run '^$' -bench . -benchmem ./pkg/channel/store ./pkg/transport ./pkg/slot/multiraft`

```text
BenchmarkTransportSend/payload=16B-10                              744.5 ns/op      213 B/op      6 allocs/op
BenchmarkTransportSend/payload=256B-10                             779.0 ns/op      213 B/op      6 allocs/op
BenchmarkTransportRPC/payload=16B-10                             21025 ns/op       1016 B/op     28 allocs/op
BenchmarkTransportRPC/payload=256B-10                            21017 ns/op       1543 B/op     28 allocs/op
BenchmarkTransportSendParallel/payload=16B-10                      810.3 ns/op      236 B/op      6 allocs/op
BenchmarkTransportSendParallel/payload=256B-10                     793.9 ns/op      235 B/op      6 allocs/op
BenchmarkTransportRPCParallel/payload=16B-10                      7746 ns/op       1051 B/op     27 allocs/op
BenchmarkTransportRPCParallel/payload=256B-10                     7486 ns/op       1579 B/op     27 allocs/op
BenchmarkRuntimeTickFanout/slots=64-10                            6518 ns/op       1028 B/op     65 allocs/op
BenchmarkRuntimeTickFanout/slots=2048-10                        190169 ns/op      49801 B/op    781 allocs/op
BenchmarkThreeNodeMultiSlotProposalRoundTrip/slots=8-10       11968254 ns/op     112877 B/op   1813 allocs/op
BenchmarkThreeNodeMultiSlotProposalRoundTrip/slots=32-10      12186644 ns/op     389432 B/op   6257 allocs/op
BenchmarkThreeNodeMultiSlotProposalRoundTripNotified/slots=8-10 3175061 ns/op     46716 B/op    732 allocs/op
BenchmarkThreeNodeMultiSlotProposalRoundTripNotified/slots=32-10 3385184 ns/op   124591 B/op   1994 allocs/op
BenchmarkThreeNodeMultiSlotConcurrentProposalThroughput/slots=8-10 375834 ns/op   26616 B/op    392 allocs/op
BenchmarkThreeNodeMultiSlotConcurrentProposalThroughput/slots=32-10 258857 ns/op  30473 B/op    471 allocs/op
PASS
```

## Known Gaps

- Baseline is captured before applying performance changes in this worktree.
- `GOWORK=off` is required inside `.worktrees/...` because the parent `go.work` points at the main checkout.
