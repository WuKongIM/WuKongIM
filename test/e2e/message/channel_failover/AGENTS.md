# channel_failover AGENTS

This file is for agents working inside
`test/e2e/message/channel_failover`.

## Scenario Purpose

This scenario proves a static three-Controller-voter `cmd/wukongim` cluster can
keep Channel quorum-acknowledged messages after one Channel leader node stops,
automatically fail over affected channels through durable migration tasks, and
fail closed for new Channel placement while the configured replica count
cannot be satisfied. Its follower-repair path adds one data-only spare, proves
the repaired spare enters the public Channel replicas and ISR while preserving
`min_isr=2`, restores the replaced source as a Controller voter, and then proves
the repaired spare can carry Channel quorum after another original replica
stops.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/channel_failover -count=1 -timeout 3m -p=1
```

## Maintenance Rules

- Keep the scenario black-box: use real `wukongim` child processes and public
  HTTP/manager APIs.
- Keep health and migration intervals short through per-node config overrides
  instead of sleeps.
- Do not inspect internal stores in this scenario; use manager message and
  Slot list surfaces for recovery assertions.
- Before stopping a second original Channel replica, restart the replaced
  source and assert through public manager state that all three original
  Controller voters are healthy and schedulable. In the same node-inventory
  snapshot, node 4 must remain data-only, fresh, alive, runtime-ready, and
  schedulable; promoting it does not preserve a majority after two original
  voters stop.
