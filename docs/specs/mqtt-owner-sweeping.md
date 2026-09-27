# MQTT owner sweep scheduling

The product must drive the existing owner deadline heap even when acquisition
never registered a connection. A local deadline fences admission; it does not
prove remote effects completed or authorize a durable Session transition.

## Failure inventory before implementation

1. Pending reservations and unregistered expired owners retain capacity forever
   because only registered connections receive automatic supervision.
2. A stale deadline closes a renewed owner; a sweep ignores its visit/time bound
   or scans every owner. Selection and fencing must remain in `Owners.Sweep`.
3. One failed, panicking or slow transport close starves other due owners. Retry
   must retain its heap entry, yield, and use a bounded cancellation deadline.
4. Cancellation or physical closure erases an admitted operation or an uncertain
   effect. Sweeping must never manufacture a quiescence/retirement receipt.
5. Repeated Start creates overlapping loops; the startup context cancels useful
   work; Stop returns while a callback still runs; a restart overlaps a timed-out
   stop. A single run owns cancellation and joined completion.
6. Shutdown/restore closes transport or cluster dependencies before the sweep
   joins. Constructor rollback must handle the worker before its first Start.
7. Owner identities, callback errors or credentials become metric labels; idle
   metrics disappear; retained barriers are falsely reported as zero on Stop.

Use actual Owners with controlled monotonic clocks and transport callback
barriers for failures unavailable through the public protocol. Keep elapsed-time
tests in the integration tier. An App integration must exercise the enabled
product's automatic pending cleanup, without manually calling Sweep. Existing
single-node and three-node Session process scenarios remain a lifecycle and
reconnect regression gate; they do not inject orphan reservations.

Frozen source context is recorded in `docs/reports/mqtt-owner-sweeping.json`.

## Implemented contract

`OwnerSweeper` owns one optional managed `mqtt/owner_sweeper` task. Each turn
calls `Owners.Sweep` with at most 256 visits and a 250ms cancellation deadline;
the default interval is 250ms. No queue, per-owner task, new table, command, RPC
or product configuration field is added. Existing synchronous admission checks
still fence an expired lease even if sweeping falls behind.

Selection, renewal and fencing remain serialized by the existing registry lock.
The existing heap moves a failed attempt to its retry deadline before yielding,
so another due owner can run on the next turn. Physical callbacks execute outside
that lock. An uncooperative callback holds the one turn until it returns; Stop
cancels but cannot discard it. A timed-out Stop retains ownership and rejects
Start until a subsequent Stop joins. Start's context does not own the run.

App starts sweeping before Gateway admission. Shutdown/restore first fences owner
admission, then joins sweeping alongside the other MQTT workers while transport
and cluster dependencies remain available. App separately closes all retained
owners before recording graceful boot retirement. The sweeper never mutates
Session rows, clears uncertain effects or issues an isolation receipt.

`wukongim_mqtt_owner_sweep_total` has only `turns`, `visited`, `failures` events.
`wukongim_mqtt_owner_work` has only `held`, `pending`, `active`, `closing`,
`operations`, `deadlines`, `uncertain` states, materialized at zero. Gauges are
last sampled aggregates, not an atomic scrape-time census; Stop does not invent
zero values for retained work. Visits count attempts, including retries, not
unique retirements. Cancellation can count as a failed turn.

## Capacity and remaining scope

Work is O(k log n) for k visited entries and O(1) for each aggregate snapshot.
The heap retains one deadline per owner under the existing reservation capacity.
The scheduler adds one goroutine/timer and one context per turn; it never adds a
per-session goroutine, scans the full registry, or stores message bodies. Large
simultaneous expiry and slow closes may take multiple turns; configured visit
bounds are not measured throughput or a 100,000-user recovery SLA. Full scale
qualification remains required.

Abrupt-crash isolation, uncertain remote-effect resolution, pending-unsubscribe
and cursorless recovery, durable record reclamation, restore reactivation and
the complete process fault/scale matrix remain open.

## Validation evidence

Before implementation, the enabled App integration failed after seven seconds:
`product never swept an expired unregistered reservation`. New runtime/metric
contracts also failed compilation before their APIs existed. The same product
case now closes the pending reservation automatically and publishes a visited
observation, then joins all MQTT tasks during Stop.

The complete default-tier runtime/app/metrics/goroutine suites pass with `-race`.
Focused race integration covers joined slow callbacks, retained operations,
uncertain barriers, bounded/fair turns, renewal and product startup/shutdown.
The existing Session process suite passes in 124.094s, with four preserved JSON
artifacts for both cluster topologies, graceful process restart and active-owner
TERM. These process cases verify lifecycle regression, not injected orphan faults.
`flow-doc-contracts` passes with 87 compliant files, zero invalid files and the
same nine existing length warnings. Commands, log hashes, source digests and
embedded process assertions are in the [evidence JSON](../reports/mqtt-owner-sweeping.json).
