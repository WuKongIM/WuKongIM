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
stops. The process-pause case retains TCP connections in a ten-Slot cluster,
limits scans to one page per tick, and distinguishes writes before process recovery from history safety after
recovery. The strict availability gate sends immediately after ACK and leader
pause while the trailing replica may be behind, then checks multiple successful
writes and an exact cross-ingress ClientMsgNo retry before resuming the process.
After resume, send through the former leader itself to exercise stale route recovery.
The strict case also pages all acknowledged history with a two-message limit,
using only More and the oldest visible sequence to cross hidden recovery barriers.
Do not weaken this gate or treat the safety case as proof of availability.

The opt-in Issue #1077 case `TestChannelLeaderFailoverDuringPromotedReplicaReplace`
holds a manager-started replica replacement at `verify_membership` through the
`wkChannelReplicaReplaceVerifyMembershipError` gofail point, stops the Channel
leader, then requires a write within 30 seconds, identical routed
`/channel/messagesync` history on every running node, and a successful write
through the restarted former leader. It must fail on binaries without the fix.
Manager message reads are node-local, so this case proves history only through
routed `/channel/messagesync`. A follower repair of the dead leader may re-add
the replaced source as the only spare; assert task completion, not its absence.

`TestChannelLeaderChangeKeepsMessageSeqContiguous` waits for the Controller's
one-way `cluster.channel_promise.activated` app-log event, kills a Channel
leader and restarts it, and requires routed `/channel/messagesync` history to be
exactly `message_seq` 1..N with every acknowledged message at its sequence: the
distributed log sequence is `message_seq`, so a leader change must consume no
sequence. It fails on barrier-mode replication (observed `[1 2 5]`). Set
`WK_E2E_CHANNEL_SEQ_REPORT=/absolute/report.json` to keep JSON evidence and
`WK_E2E_CHANNEL_SEQ_LOG_ROOT=/absolute/dir` to retain node logs.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/channel_failover -count=1 -timeout 6m -p=1
```

Issue #1077 gofail case (opt-in, needs a gofail-enabled binary; nightly `ci.yml` runs it through `go run ./cmd/wkcicheck gofail-e2e`):

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongim --package pkg/cluster/channels --out /tmp/wukongim-gofail-channels
WK_E2E_BINARY=/tmp/wukongim-gofail-channels WK_E2E_GOFAIL_CHANNEL_MIGRATION=1 \
  WK_E2E_REPLICA_REPLACE_FAILOVER_REPORT=/tmp/replica-replace-failover.json \
  GOWORK=off go test -tags=e2e ./test/e2e/message/channel_failover \
  -run TestChannelLeaderFailoverDuringPromotedReplicaReplace -count=1 -timeout 8m -p=1
```

`WK_E2E_REPLICA_REPLACE_FAILOVER_REPORT` writes the JSON artifact (held task,
write-recovery time, post-failover topology and final history);
`WK_E2E_REPLICA_REPLACE_LOG_ROOT` optionally retains node logs.

## Maintenance Rules

- Keep the scenario black-box: use real `wukongim` child processes and public
  HTTP/manager APIs.
- Keep health and migration intervals short through per-node config overrides.
  The pause case waits one bounded health TTL before sending because remote
  node inventory may itself wait on the paused peer. Resume the exact owned
  process in cleanup before cluster teardown.
- HTTP-only scenarios use `WaitHTTPReady`; they do not use an unauthenticated
  WKProto readiness handshake or disable client token authentication.
- Do not inspect internal stores in this scenario; use manager message and
  Slot list surfaces for recovery assertions.
- Before stopping a second original Channel replica, restart the replaced
  source and assert through public manager state that all three original
  Controller voters are healthy and schedulable. In the same node-inventory
  snapshot, node 4 must remain data-only, fresh, alive, runtime-ready, and
  schedulable; promoting it does not preserve a majority after two original
  voters stop.

- The Channel failover fixture uses 256 Hash Slots and explicitly disables Token authentication for its existing tokenless readiness client. SDK acceptance keeps Token authentication enabled.
