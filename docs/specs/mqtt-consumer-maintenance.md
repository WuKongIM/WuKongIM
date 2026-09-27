# MQTT consumer maintenance

## Failure inventory before implementation

1. Offline Sessions never execute accounting, so message/byte quotas are enforced
   only after reconnect. Real process tests must observe quota ending while the
   receiver remains disconnected, then require Session Present 0 and resubscription.
2. Full receive windows prevent accounting; shared content accumulates despite
   configured limits. Maintenance must not require owner execution or send credit.
3. Discovery uses only online clients, stale per-process source lists or an
   unbounded map. Use existing Slot-owned source-binding recovery index pages;
   all candidates are hints and each turn rereads authoritative state.
4. Wrong/partial/unordered recovery pages, malformed keys or late/cancelled reads
   dispatch effects. Validate the complete bounded page before scheduling.
5. One failed consumer or full cohort starves the remaining keys. Bound retained
   keys across queued/executing work, preserve unadmitted cursors, yield after
   attempts and discard lost-leadership hints without assuming completion.
6. Accounting follows a replaced lifetime, mistakes infrastructure failure for
   revocation, or quota cleanup targets a new owner. Exact revisions/generations
   remain in Accounting and End; capture the owner and never follow a successor.
7. Successful PUBACKs never project completion, or ended consumers never release
   source obligations. Compose existing SourceProgress and SourceRemoval; absent
   state and RPC failure cannot discharge responsibility or grant content GC.
8. Ending after a lost quota reply is forgotten because the Session no longer
   appears in a live deadline index. Binding recovery remains authoritative work
   until removal; confirm exact ended owner cleanup before recording its result.
9. Stop cancels without joining workers, starts a second cohort or tears down
   dependencies early. Join scanner/cohort before connections, Gateway and cluster.
10. Metrics label client/source identities or imply exact unique termination
    counts from retried work. Publish fixed aggregate turn/outcome observations;
    successful quota-end observations allow deterministic public E2E waiting.

The initial composition maintains Channel bindings, including person sources
qualified through UID inbox bindings. Subsequent [ended-UID retirement](mqtt-qualification-retirement.md) reuses this cohort; subsequent [pending removal recovery](mqtt-pending-removal-recovery.md) adds closed-intent discovery and completion. No new table, index or RPC format is needed.

All maintenance uses cluster authority, including single-node clusters; tests use
256 hash Slots. Usecase policy, runtime scheduling, app wiring and metrics remain
in their existing layers. No network publication or inflight admission is added.

## Composition and bounds

`ConsumerMaintenance` handles one Channel binding per turn. It rereads current
subscription and parent Session, invokes existing bounded original-content
Accounting, then SourceProgress and at most one SourceRemoval step. Definite
receive denial ends only the captured Owner; unavailable authority and changed
ownership yield without following a successor. An already-ended quota/revocation
row repeats exact cleanup, covering committed-but-lost accounting replies.

`ConsumerWorker` rotates existing recovery-index pages over currently led hash
Slots. Defaults are 32 pages per scan, 16 rows per page, 200 ms cadence, a two-second
scan budget, 250 ms per read and five seconds per execution. Configured
`mqtt.workers` bounds queued plus executing keys (default 16, maximum 128).
There is no per-Session goroutine, payload queue or unbounded discovery map.
Product composition scans two streams per Slot, bounding retained cursors at twice the configured hash-Slot count, normally 512.
A full cohort retains the last admitted row cursor; failed rows retry on wrap.
This is eventual maintenance, not a promised quota-enforcement latency at scale.

App starts maintenance after cluster readiness and joins it before closing its
dependencies. Cancellation cannot substitute for joining admitted effects.
Two fixed task labels register scheduler and worker ownership. Metrics expose
`wukongim_mqtt_consumer_events_total{event}` with twelve predeclared outcomes (including subsequent UID retirement and subscription-removal confirmation) and
`wukongim_mqtt_consumer_work{state}` with admitted/capacity series. No ClientID,
UID, source, payload or raw error becomes a label. End confirmations can repeat.

## Discovered pagination regression

The first product-process run discovered no consumers despite successful scans.
Public metrics showed positive pages/failures but zero visited/scheduled keys.
A bounded temporary probe isolated final-page cursor validation: generic metadata
ScanIndex may retain the request cursor on a terminal page containing rows.
The failure-first regression reproduces this with cohort pressure between two
rows. Validation now accepts that terminal shape while retaining exact row
continuations under pressure; partial pages still require their last row cursor.
Temporary probes were removed. Existing deadline scanning uses the same rule.

## Evidence scope

The process suite observes offline message/byte quota endings before reconnect,
quota ending with one unacknowledged exchange and Receive Maximum 1, and ACK
progress followed by unsubscribe/source removal. It runs against single-node
and three-node clusters with 256 hash Slots, aggregating only public fixed metrics.
After quota ending, reconnect must report Session Present 0 and resubscription
must deliver a new message. Artifacts contain assertions, never message bodies.

The original checks do not prove complete UID-binding/cursorless cleanup, inflight/accounting-row
reclamation, content GC, global storage pressure, abrupt owner-crash isolation,
restore reactivation or high-scale throughput. Those remain full-goal work.

Validation on this change:

- Eight process scenarios passed in 206.545 s; repeat with
  `WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-consumer-reports GOWORK=off go test -p 2 -tags=e2e ./test/e2e/mqtt/quota -count=1 -timeout=8m -v`.
- Persistent Session/takeover/graceful restart/TERM regression passed in 131.043 s;
  Will Delay/normal cancellation regression passed in 57.871 s.
- Full usecase/runtime/app/metrics/goroutine race suites passed. After the terminal
  cursor correction, full runtime race and joined-cohort integration race passed.
- `flow-doc-contracts`: 87 compliant, zero invalid, nine existing length warnings.
- Frozen inputs, RED results, commands, log hashes and embedded process assertions
  are in [the evidence report](../reports/mqtt-consumer-maintenance.json).
