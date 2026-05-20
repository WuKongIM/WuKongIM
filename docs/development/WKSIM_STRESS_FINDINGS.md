# wk-sim Stress Findings

This document records issues found while running the Docker Compose `wk-sim`
load loop. Keep entries concise so the final review is easy to scan.

## 2026-05-20 Run 1

Environment:
- Worktree: `.worktrees/wksim-bug-hunt-20260520`
- Baseline unit command: `GOWORK=off go test ./...`
- Compose command: `WK_SIM_VERIFY_RECV=sampled WK_SIM_TRAFFIC_CONCURRENCY=128 docker compose --profile dev-sim up -d --build wk-sim`
- Workload: 1000 users, 500 person channels, 500 group channels, 10 group members, 0.25/s per channel.

### Issue 1: verified receive traffic double-sent recvack frames

Evidence:
- Under sampled receive verification, `/status` repeatedly entered `retrying`.
- Example error: `person recvack: write tcp ...:5100: i/o timeout`.
- Counters after the first minute showed send and receive errors while CPU remained low, pointing to protocol write/backpressure rather than node saturation.

Root cause:
- `StartAutoRecvAck` enables automatic recvack on `matchingPersonClient`.
- Verified person/group workloads still called `RecvAck` explicitly after reading the same recv frame.
- A verified recv could therefore be acknowledged once by the matching reader and again by the explicit workload call.

Fix:
- When automatic recvack is enabled on `matchingPersonClient`, explicit `RecvAck` calls are treated as already handled by the matching reader.
- Regression test: `TestAutoRecvAckSuppressesDuplicateExplicitRecvAck`.

### Issue 2: dev-sim status kept stale readiness errors after target became ready

Evidence:
- `/status` stayed in `waiting` for more than a minute during prepare/connect and continued to show initial `connection refused` errors even after all node `/healthz`, `/readyz`, and `/bench/v1/capabilities` endpoints returned 200.

Root cause:
- `Runner.waitReady` stored every transient readiness error but only cleared `last_error` after the simulator reached `running`.
- Slow prepare/connect made the status API report stale target failures during successful startup work.

Fix:
- Clear `last_error` immediately after target readiness succeeds.
- Regression test: `TestRunnerClearsTransientReadinessErrorBeforePrepare`.

### Issue 3: person send path times out refreshing channel metadata under startup load

Status: observed, next investigation cycle.

Evidence:
- After rebuilding with the recvack fix, sampled verification remained `running` instead of continuous `retrying`, but counters still showed send and receive errors.
- Node logs repeatedly showed `message.send.refresh.failed` for person channels with `context deadline exceeded`, followed by gateway `send_failed` warnings.
- Example channel: `devsim-u-67@devsim-u-66`, channel type `1`.

Initial hypothesis:
- The person-channel metadata refresh path is missing a fast negative/creation cache or has insufficient concurrency during many cold person-channel sends.
- This will be investigated with a focused unit test before any production change.

## 2026-05-20 Run 2

Environment:
- Existing three-node Compose cluster from Run 1.
- Isolation profiles used `--no-build --force-recreate wk-sim` with unique `WK_SIM_UID_PREFIX` values.

### Issue 3 update: person metadata refresh timeout was not reproduced on clean data

Evidence:
- After cleaning `docker/dev-cluster` and `docker/dev-sim`, the default smoke command passed with `connected_users=1000`, `messages_sent=2209`, `send_errors=0`, and `recv_errors=0`.
- A person-only sampled profile (`40` users, `10` person channels, `0` groups, `0.5/s`, concurrency `16`) ran for more than one minute and reached `messages_sent=348`, `send_errors=0`, `recv_errors=0`.

Status:
- Keep as a non-reproduced startup/cold-state observation. No production fix was made in this cycle.

### Issue 4: group sampled verification only reached the local delivery-tag partition

Evidence:
- A group-only sampled profile (`40` users, `0` person channels, `3` groups, `12` members, `0.5/s`, concurrency `16`) repeatedly alternated between `running` and `retrying`.
- `/status` stabilized at `messages_sent=2`, `send_errors=0`, `recv_errors=2`, and `last_error="context deadline exceeded"`.
- A temporary WKProto probe prepared one group with four subscribers across three gateways. The sendack succeeded, but only the subscriber connected to the sender/owner node received the group `RecvPacket`; subscribers on the other two nodes timed out.

Root cause:
- `tagDeliveryResolver` expanded only the current node's delivery-tag partition (`deliveryTagLocalUIDs`) when a leader tag contained multiple node partitions.
- The channel owner submitted realtime fanout immediately only for that local partition, so remote partition recipients depended on slow replay paths instead of receiving the committed message promptly.

Fix:
- Resolve every UID present in the current node-local tag body when the leader has the full tag, while follower-local tag bodies still contain only their stored partition.
- Regression test: `TestDeliveryRoutingExpandsAllLeaderTagPartitionsForFanout`.

Verification:
- Targeted routing tests passed with `GOWORK=off go test ./internal/app -run 'TestDeliveryRouting|TestDistributedDeliveryPush|TestLocalDeliveryPush' -count=1`.
- Broader focused tests passed with `GOWORK=off go test ./internal/app ./internal/bench/... ./cmd/wkbench -count=1`.
- Rebuilt Compose group-only sampled profile with prefix `groupfix-u` stayed `running` for more than 5 minutes and reached `messages_sent=494`, `send_errors=0`, `recv_errors=0`, `last_error=""`.
