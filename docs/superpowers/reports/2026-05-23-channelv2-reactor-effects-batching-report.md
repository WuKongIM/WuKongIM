# Channelv2 Reactor Effects And Batching Report

## Commands

- `GOWORK=off go test ./pkg/channelv2/reactor -run TestObserverSeesAppendBatchAndWorkerResult -count=1`
- `GOWORK=off go test ./pkg/channelv2 -run '^$' -bench 'BenchmarkAppendSingleNodeHotChannelBatched|BenchmarkAppendSingleNodeManyChannelsAsync|BenchmarkAppendThreeNodeManyChannelsAsync' -benchtime=1s -count=1`
- `GOWORK=off go test ./pkg/channelv2/reactor ./pkg/channelv2/worker ./pkg/channelv2/service -count=1`
- `GOWORK=off go test ./pkg/channelv2/... -count=1`
- `rg 'pkg/channel"|pkg/channel/store' pkg/channelv2 -g'*.go'`
- `git diff --check`

## Results

```text
BenchmarkAppendSingleNodeHotChannelBatched-10        4706    292291 ns/op      2195 append-latency-us       114.6 bytes/batch         0 goroutine-delta        10.00 max-mailbox-depth         0 max-worker-queue         7.640 records/batch         0.1309 worker-queue-samples/op         0.1309 worker-results/op    127721 B/op      27 allocs/op
BenchmarkAppendSingleNodeManyChannelsAsync-10       3433    323296 ns/op      2322 append-latency-us        15.00 bytes/batch         0 goroutine-delta         4.000 max-mailbox-depth         6.000 max-worker-queue         1.000 records/batch         1.000 worker-queue-samples/op         1.000 worker-results/op    202312 B/op      47 allocs/op
BenchmarkAppendThreeNodeManyChannelsAsync-10        5866    215757 ns/op         0 goroutine-delta    145263 B/op      55 allocs/op
```

## Observations

- The hot-channel benchmark batched appends in this smoke run: it reported `7.640 records/batch` and `114.6 bytes/batch`.
- The many-channel single-node benchmark mostly flushed one record per channel batch in this run: it reported `1.000 records/batch` and `15.00 bytes/batch`.
- Observer-derived worker metrics were emitted by the single-node benchmarks: hot-channel reported `0.1309 worker-results/op`, and many-channel reported `1.000 worker-results/op` plus a `6.000 max-worker-queue` sample.
- Allocation counts from the smoke rows were `27 allocs/op`, `47 allocs/op`, and `55 allocs/op` for the three reported benchmarks.
