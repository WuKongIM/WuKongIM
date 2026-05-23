# Channelv2 Replication Robustness Report

## Commands

- `GOWORK=off go test ./pkg/channelv2/reactor -run 'TestFollower|TestLeader|Test.*Replication|Test.*Ack|Test.*Pull' -count=1`
- `GOWORK=off go test ./pkg/channelv2/testkit -run 'TestThreeNode|Test.*Replication|Test.*Ack|Test.*Pull' -count=1`
- `GOWORK=off go test ./pkg/channelv2/... -count=1`
- `GOWORK=off go test -race ./pkg/channelv2/... -count=1`
- `rg 'pkg/channel"|pkg/channel/store' pkg/channelv2 -g'*.go'`
- `GOWORK=off go test ./pkg/channel/... ./pkg/channelv2/... -count=1`
- `gofmt -w pkg/channelv2`
- `git diff --check`

## Results

- PASS: `GOWORK=off go test ./pkg/channelv2/reactor -run 'TestFollower|TestLeader|Test.*Replication|Test.*Ack|Test.*Pull' -count=1` (`ok github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor 0.839s`).
- PASS: `GOWORK=off go test ./pkg/channelv2/testkit -run 'TestThreeNode|Test.*Replication|Test.*Ack|Test.*Pull' -count=1` (`ok github.com/WuKongIM/WuKongIM/pkg/channelv2/testkit 0.630s`).
- PASS: `GOWORK=off go test ./pkg/channelv2/... -count=1` for all channelv2 packages (`channelv2 0.849s`, `machine 1.172s`, `reactor 1.363s`, `service 1.349s`, `store 1.750s`, `testkit 2.242s`, `worker 0.852s`).
- PASS: `GOWORK=off go test -race ./pkg/channelv2/... -count=1` for all channelv2 packages (`channelv2 1.360s`, `machine 1.701s`, `reactor 2.096s`, `service 2.889s`, `store 2.596s`, `testkit 3.278s`, `worker 2.138s`). macOS linker emitted accepted `malformed LC_DYSYMTAB` warnings for race test binaries.
- PASS: `rg 'pkg/channel"|pkg/channel/store' pkg/channelv2 -g'*.go'` returned only `pkg/channelv2/store/channel_adapter.go` imports/comments.
- PASS: `GOWORK=off go test ./pkg/channel/... ./pkg/channelv2/... -count=1` for old and new channel package tests (`channel 2.223s`, `channel/handler 5.691s`, `channel/replica 3.087s`, `channel/runtime 3.846s`, `channel/store 10.691s`, `channel/transport 5.469s`, `channelv2 3.880s`, `channelv2/machine 3.991s`, `channelv2/reactor 4.474s`, `channelv2/service 3.870s`, `channelv2/store 2.001s`, `channelv2/testkit 1.070s`, `channelv2/worker 0.160s`).
- PASS: `gofmt -w pkg/channelv2` produced no working tree changes.
- PASS: `git diff --check` returned no whitespace errors.

## Notes

- The phase keeps the existing short-poll `Pull` plus explicit `Ack` protocol.
- The phase does not add snapshot, retention, migration, leader repair, or `internal/app` integration.
