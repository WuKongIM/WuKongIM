# MQTT storage capacity during a three-node network partition

Status: passed for the recorded macOS arm64 partition shape. Base:
`eb4c70b270193b22a520b8fe06b44ec72b19f333`.
Receipts and repeat instructions: [acceptance report](../reports/mqtt-storage-partition/README.md).

## Approved boundary and failure inventory

The operator approved real-process, local three-node E2E with 256 hash Slots.
Observe public MQTT 5, WKProto/Product HTTP receipts, Manager inventories and
fixed Prometheus capacity signals. Do not read storage rows or invoke usecases.
Use a test-side TCP relay; processes, public ports and timers remain live.
This task does not buy cloud resources, change membership or qualify a release.

Failures to detect before changing product code:

1. A live isolated node or surviving quorum accepts new unfunded content.
2. Disconnect/timeout/absence refunds accepted or physically uncertain debt.
3. Recovery resets accounting, loses an accepted body, changes its identity,
   Packet Identifier or unfinished QoS 1 DUP replay, or requires resubscription.
4. One subscriber's ACK frees a body still needed by an independent Session.
5. Proved completion never retires charges or never reopens admission.
6. A supposed partition leaves existing/reconnected cluster TCP links usable,
   stops the process, blocks public observation, or leaks relay/client workers.

## Vertical acceptance slices

First establish two persistent independent consumers, one shared accepted
2048-byte source and a ceiling that cannot fund a second source. Isolate one
node in both TCP directions, prove its process and public metrics stay live,
and observe at least 35 seconds (beyond the two-second readiness proof and
the 30-second Session ownership grant). Send once through every public ingress;
none may report success. Capacity stays at least the accepted obligation and
within node/cluster ceilings. The two survivors must retain public Slot quorum.

Heal the same links without restarting any process. Reconnect the unfinished
Session without SUBSCRIBE, preserve native identity and Packet Identifier/DUP,
ACK one consumer and prove the other still protects the shared body. Complete
the second consumer, observe zero reserved bytes, then require a fresh delivery.

The next slice deliberately separates an unknown physical preparation from
accepted data. A temporary-copy gofail candidate pauses an existing physical
preparation boundary; require an actual hit before cutting TCP. Observe retained
positive-charge debt after an independent successful after-commit witness
increments exactly once while TCP remains cut; reserved gauges predate commit
and cannot establish physical completion. Keep that debt until the exact
preparation is resolved after healing: cancellation may
return unused credit, or an exact keyed continuation may consume its funded
ticket. Require rejected-source quiet before retry, one accepted continuation
and zero debt only after consumer completion. A cut
that did not reach the intended boundary is a setup failure, not qualification.

## Harness constraints

Relay addresses are the static membership endpoints; actual product listeners
are separate. A bounded `lsof` query maps each accepted TCP source socket to one
of the exact test-owned process IDs; unidentified sockets are refused. This
does not decode frames or inspect business state. All links involving the
selected node are closed and reconnects refused atomically. Node-local TCP and
the other two nodes' links remain usable. Record per-directed-link forwarded/refused/closed counts, bounded metric
snapshots, phases and stable public identities; never credentials or payloads.
Require `lsof` explicitly; missing prerequisites fail rather than silently skip.
Qualification covers the recorded OS only. Join listeners, relay workers and
product process groups before publishing the final JSON receipt.
