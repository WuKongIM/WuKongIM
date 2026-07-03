# channelv2_failover AGENTS

This file is for agents working inside
`test/e2ev2/message/channelv2_failover`.

## Scenario Purpose

This scenario proves a static three-node `cmd/wukongim` cluster can keep
ChannelV2 quorum-acknowledged messages after one ChannelV2 leader node stops,
automatically fail over affected channels through durable migration tasks, and
fail closed for new ChannelV2 placement while the configured replica count
cannot be satisfied.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/message/channelv2_failover -count=1 -timeout 3m -p=1
```

## Maintenance Rules

- Keep the scenario black-box: use real `wukongim` child processes and public
  HTTP/manager APIs.
- Keep health and migration intervals short through per-node config overrides
  instead of sleeps.
- Do not inspect internal stores in this scenario; use manager message and
  Slot list surfaces for recovery assertions.
