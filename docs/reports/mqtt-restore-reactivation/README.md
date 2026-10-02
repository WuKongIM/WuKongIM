# MQTT restore reactivation acceptance

Source base: `aab707786ddec960289f6582027bdd962d96911f`, reused
`codex/mqtt-design` worktree. Applicable source-revision instruction/FLOW hashes
and approved public seams are frozen in [context](frozen-context.json).
[Candidate provenance](candidate-provenance.json) records actual source bytes,
Go 1.25.11 and the ordinary product binary SHA-256; no embedded VCS stamp is
used to identify the worktree build.

## Behavior and scope

The stable Gateway/Owner RPC composition replaces terminal MQTT generations
only after CONNECT handoffs, workers, physical connections and admitted Owner
operations join. Restore connection teardown proves exact local isolation and
does not require a new durable Session disconnect against already-fenced peers.
Ordinary application shutdown still performs that durable lifecycle mutation.
Fresh random boots and complete worker cohorts publish before maintenance ends;
accepted old connections remain bound to their original handler.

The operator approved authenticated Manager backup/restore, independent Paho
MQTT 5 and Product HTTP provisioning/send/history. Tests use real 256-hash-Slot
clusters and an existing native group history. Each archives one unfinished QoS 1
exchange, adds post-backup state, then restores twice without process restart.
There is no resubscription. Each boundary races 16 bounded CONNECTs, refuses a
maintenance CONNECT, proves old sockets closed, and checks Session Present,
original Packet ID/body/message identity, DUP replay, removal of post-backup
state and one fresh delivery. The quiet-window extra-delivery check is 300 ms;
it is not an unbounded exactly-once claim.

## Evidence

- [Baseline RED](baseline-red.json): original single-node product remains in
  Finalizing because MQTT requires process restart. Bounded run failed at
  restore completion (187.81 s). The earlier interrupted exploratory run and
  fixture compilation failures are excluded.
- [Intermediate single-node](intermediate-single-node.json): two restores passed
  with CONNECT handoff joining (234.65 s), before the multi-node teardown repair.
- [Three-node stop failure](diagnostic-three-node.json) and
  [bounded signals](diagnostic-signals.txt): authentication and preceding workers
  join, then the owning node retries durable disconnect and cannot finish the
  connection worker. Prepare reports maintenance inactive, and the connection
  stop ultimately times out. The probe does not preserve `cluster.ErrMaintenance`
  through the remote error boundary; attribution to fenced peer metadata is an
  inference from that boundary and the cluster admission contract.
- [Joined-node signals](joined-node-signals.txt): after the repair every node
  completes MQTT and other side-effect joins and local cluster pause. The
  [four-minute diagnostic deadline](diagnostic-progress-budget.json) expires
  while restore keeps progressing; public dashboard samples observed staging
  and then 174 verified partitions. This is a budget failure, not a passed run.
- [Final single-node](single-node.json): both complete cycles pass using the
  ordinary candidate binary and the final readiness-aware fixture (194.28 s).
  [Earlier ordinary single-node evidence](earlier-single-node.json) also passed
  before the readiness observation and convergence-budget refinements (250.26 s).
- [Immediate CONNECT diagnostic](immediate-connect-failure.json): one complete
  three-node cycle passed; the second restore succeeded but an immediate CONNECT
  returned EOF. One later diagnostic CONNECT succeeded, while the original
  failure remained red (793.37 s). [Isolation signals](connect-isolation-signals.txt)
  prove the sampled old-boot RPCs succeeded; absence of a captured acquisition
  error does not prove why the transport closed. The final fixture awaits each
  node’s public HTTP readiness after Controller completion, then connects once.
- [Two-minute single-node convergence failure](single-node-insufficient-budget.json):
  first restore was still Switching when its completion bound expired. The final
  single-node convergence budget is four minutes; foreground MQTT deadlines stay
  unchanged. Restore throughput on the shared development host is unqualified.
- [Final three-node](three-node.json): both complete cycles pass using the
  ordinary candidate binary (718.41 s), after every node’s public HTTP readiness.
  The exact compiled fixture digest is retained separately; the final source
  differs only in the single-node convergence budget and a coverage comment.
  Three-node bounds and all assertions are identical.

[Focused race suites](race.log), [full app race suite](app-race.log),
[app integration](integration.log), [retained isolation failures](isolation.log),
[process Session/crash regressions and suite helpers](regression-session.log),
and the named [flow-doc-contracts](flow-check.log) check passed.
The integration sweep fixture now pins the published generation; its existing
pending-expiry, cleanup and shutdown assertions remain unchanged.

## Separate setup findings

Two ordinary three-node runs returned a generic restore HTTP 503 before the
fixture observed maintenance. [Captured response failure](restore-response-503.json)
does not prove whether a restore was admitted. No mutation was retried. A later
bounded preflight probe reported successful checks on all nodes with about
19.3 GB available versus about 1.1 GB required and 27 MB current data; this
excludes capacity rejection for that run, not for every earlier request. The
original generic response cause remains unconfirmed.

The first three-node empty-history group SUBSCRIBE intermittently times out
before restore; captured observations include canceled, deadline and zero
sampled closure partitions. See [candidate failure 1](candidate-cold-subscribe-failure.json)
and [candidate failure 2](candidate-cold-subscribe-failure-2.json). An
existing-history run also failed before backup with one deadline closure
([capture](candidate-subscribe-deadline.json)); it overlapped another cluster
workload. Root cause and relationship to historical cold admission remain
unconfirmed. No foreground retry or packet deadline was relaxed. Existing-history restore acceptance does
not qualify cold admission. The finding is also in `CODE_QUALITY.md`.

A committed backup plan must be exposed by all Controller mirrors before the
repository test resolves it. The reusable helper polls exact public revisions
and never retries ambiguous plan or restore mutations. Missing cluster readiness
and missing plan-mirror observation in intermediate fixtures are excluded from
product-failure conclusions.

## Reproduce

From this worktree, with the available Go toolchain/module cache:

```sh
GOWORK=off go build -buildvcs=false -o /tmp/wukongim-mqtt-restore ./cmd/wukongim
WK_E2E_BINARY=/tmp/wukongim-mqtt-restore WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-restore-reports GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/restore -count=1 -timeout=25m -v
```

Convergence is bounded to four minutes per phase for one node and eight minutes
for three nodes, with a twenty-minute scenario context. A three-node pass stages,
verifies and switches 768 replica partitions. Foreground budgets remain intact.
Raw configuration/application logs stay outside Git. Only bounded fixed-label
probe signals and normalized test outcomes are retained here. Probes were built
with external Go overlays; no debug instrumentation is in product code.

## Coverage limits

Builder/startup failure and concurrent terminal Stop are not newly fault-injected
at the restore process seam. Existing isolation tests verify retained unknown
or failed physical cleanup; they do not establish safe redispatch of unknown
external Will effects. Unavailable nodes/partitions, exhaustive Will acceptance,
restore performance qualification and a new 100k workload run are outside this
acceptance. Local old-boot receipts never infer isolation of an unreachable node.
