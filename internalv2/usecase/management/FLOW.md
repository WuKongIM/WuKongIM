# internalv2/usecase/management Flow

## Responsibility

`internalv2/usecase/management` builds entry-independent read models for the
new manager API. It currently owns the node list, Slot list, business channel
list, channel runtime metadata list, Controller/Slot distributed log pages,
Controller task audit history, Controller Raft status and explicit compaction orchestration,
Controller voter promotion validation, Slot Raft
explicit compaction orchestration, Slot leader-transfer intent
validation/submission, recent conversation list, channel message list,
message retention adapter contract, local-or-remote connection list/detail
projection, local-or-remote node plugin list/detail projection,
cluster-authoritative plugin binding list/mutation, DB Inspect,
diagnostics trace/message/event query orchestration and tracking-rule fan-out,
node-local diagnostics orchestration, node lifecycle join/activation/leaving,
bounded Slot onboarding, pure Slot move-out, scale-in preparation status and final safe removal,
dynamic node root-cause diagnostics,
user management, and system UID projections/actions used by
`GET /manager/nodes`,
`POST /manager/nodes/join`, `POST /manager/nodes/:node_id/activate`,
`POST /manager/nodes/:node_id/onboarding/plan`,
`POST /manager/nodes/:node_id/onboarding/start`,
`GET /manager/nodes/:node_id/onboarding/status`,
`POST /manager/nodes/:node_id/onboarding/advance`,
`POST /manager/nodes/:node_id/slot-move-out/plan`,
`POST /manager/nodes/:node_id/slot-move-out/advance`,
`POST /manager/nodes/:node_id/scale-in/plan`,
`POST /manager/nodes/:node_id/scale-in/start`,
`POST /manager/nodes/:node_id/scale-in/drain`,
`POST /manager/nodes/:node_id/scale-in/remove`,
`GET /manager/nodes/:node_id/scale-in/status`,
`POST /manager/nodes/:node_id/scale-in/advance`,
`GET /manager/nodes/:node_id/diagnostics`,
`GET /manager/slots`, `POST /manager/slots/:slot_id/leader-transfer`,
`GET /manager/channels`,
`GET /manager/channel-runtime-meta`, `GET /manager/controller/logs`,
`/manager/channel-migrations*`,
`GET /manager/controller/tasks`, `GET /manager/controller/tasks/:task_id`,
`GET /manager/controller/task-audits`,
`GET /manager/controller/task-audits/:task_id/events`,
`GET /manager/slots/:slot_id/logs`,
`GET /manager/nodes/:node_id/controller-raft`,
`POST /manager/nodes/:node_id/controller-raft/compact`,
`POST /manager/controller-raft/compact`,
`POST /manager/nodes/:node_id/controller-voter/promote`,
`POST /manager/nodes/:node_id/slots/:slot_id/compact`,
`GET /manager/conversations`, `GET /manager/messages`,
`POST /manager/messages/retention`, `/manager/connections*`,
`/manager/nodes/:node_id/plugins*`, `/manager/plugin-bindings`,
`/manager/db/inspect*`, `/manager/diagnostics*`, `/manager/users*`, and
`/manager/system-users*`.

Plugin binding list requests accept exactly one selector. UID requests call the
cluster-authoritative binding store directly. Plugin-number requests require
the store to implement the optional plugin-centric scanner and preserve its
opaque cursor/`has_more` values; the usecase only normalizes limits, enriches
rows with local plugin runtime warnings, and keeps response shaping independent
from HTTP.

## Node List Flow

```text
manager HTTP handler
  -> management.App.ListNodes
  -> ControlSnapshotReader.LocalControlSnapshot
  -> clusterv2 control snapshot
  -> SlotRuntimeStatusReader.SlotRuntimeStatus
  -> RuntimeSummaryReader.NodeRuntimeSummary
  -> sorted manager node DTO rows
```

The projection derives node identity, health, durable lifecycle, capacity
weight, controller role, and desired Slot replica counts from the local
clusterv2 control snapshot. Health fields are copied from `control.Node.Health`
so operators can see whether evidence is fresh, stale, or missing, whether
runtime readiness is true, the report age/TTL, observed control and Slot
revisions, and the bounded health error code. `membership.schedulable` uses
`control.NodeSchedulableForPlacement`, so it matches the actual placement
predicate: data role, active lifecycle, fresh `alive` health, and
`runtime_ready=true`. Slot leader counts are best-effort live Raft observations
from `SlotRuntimeStatusReader`; they do not fall back to `PreferredLeader`, so
the node list does not mix control-plane placement intent with actual Raft
leadership. Runtime online and gateway counters are read through the narrow
`RuntimeSummaryReader` port. Read failures or an unwired runtime source mark
only that node's runtime summary as unknown. Node list action hints remain
read-model hints. `can_move_slots_in` and `can_move_slots_out` are enabled for
active Data-role nodes, including nodes that are also Controller voters, because
Slot replica migration is separate from node removal. `can_scale_in` and
`can_promote_controller_voter` stay limited to active or leaving data-only
nodes as appropriate; scale-in/remove and Controller membership changes still
run their own usecase safety gates. `can_drain` and `can_resume` stay tied to
the legacy node-operation routes and remain disabled in internalv2 until those
routes are migrated. Stage 5C gateway drain mode is exposed through the
scale-in route and status instead of these node-list action hints. Lifecycle
writes are exposed separately by the join and activate flow below.

## Controller Voter Promotion Flow

```text
manager HTTP handler
  -> management.App.PromoteControllerVoter
  -> ControlSnapshotReader.LocalControlSnapshot
  -> ControllerVoterReadinessReader for target readiness
  -> ControllerVoterPreparer for target-side live Controller Raft proof
  -> ControllerVoterPromoter for clusterv2 control promotion write
```

Controller voter promotion uses one local control snapshot as the durable
decision base. The usecase fails closed before target preparation when the
target is missing, not an active data-only node, lacks a cluster address, has
stale or non-alive health, is not runtime-ready, or has not observed the
snapshot revision. Existing Controller voters are treated as idempotent no-ops.
For non-voters, the usecase builds the previous durable Controller voter set
and next voter endpoints from that snapshot, then requires target readiness and
target-side preparation proof. The final control writer request carries the
snapshot revision, the previous durable voter set as a non-nil fence, and the
observed Controller Raft config index and voter set returned by preparation.
Missing promotion ports are reported as unavailable; failed safety gates are
reported as blocked with bounded machine-readable reasons. HTTP routing stays
outside this usecase. The target readiness and preparation ports are supplied
by the node lifecycle RPC/app wiring path, and this usecase consumes only their
entry-independent readiness and live-proof DTOs.

After each promotion attempt, the optional
`ControllerVoterPromotionObserver` receives one bounded result
(`changed`, `noop`, `blocked`, or `unavailable`), one bounded blocker reason
when the result is blocked, and phase durations for the real usecase
boundaries (`readiness`, `prepare`, and `commit_state`). Metrics consumers may
predeclare the full Controller membership flow phases, including
`add_learner`, `catch_up`, and `promote_voter`, but the usecase only records
phases it actually executes. The observer never receives target node IDs,
addresses, task IDs, or cluster IDs as metric labels.

`ControllerRaftStatus` returns live Controller Raft membership evidence from
the selected node, including voter and learner sets. A successful status read
is passed to the optional `ControllerRaftStatusObserver` so app-level metrics
can publish current voter/learner counts from status collection rather than
from durable role hints.

## Node Lifecycle Flow

```text
manager HTTP handler
  -> management.App.JoinNode/ActivateNode/MarkNodeLeaving/MarkNodeRemoved
  -> NodeReadinessReader for activation only
  -> NodeLifecycleWriter
  -> clusterv2 control lifecycle writer
```

The lifecycle usecase validates manager-facing node IDs and join addresses,
then delegates cluster-authoritative mutations to the narrow control writer
port. Join requests always request the data role and leave capacity-weight
defaulting to the control writer. Activation first reads the local control
snapshot, requires the target node to exist in `joining` state, probes the
target node through `NodeReadinessReader`, and only delegates the active write
when the target is reachable, transport/control/runtime ready, mirrored to the
expected cluster ID, and caught up to at least the observed control revision.
Leaving requests validate only the manager-facing node ID and delegate the
durable transition to the control writer; ControllerV2 remains responsible for
rejecting controller voters and invalid lifecycle transitions.
Control conflicts, missing-node activation errors, and not-ready activation
checks are mapped to dedicated usecase errors so HTTP can distinguish operator
input conflicts, infrastructure failures, and readiness gates. Not-ready errors
include the failed readiness fields and compact target error text so operators
can tell transport, control mirror, runtime, cluster-id, and revision gates
apart without guessing from a generic conflict. When the
lifecycle writer or readiness reader is not configured, the usecase returns a
dedicated unavailable error so HTTP can report `service_unavailable` instead of
treating wiring as invalid operator input.
Lifecycle usecase methods do not seed node RPC, poll startup, rebalance Slots,
or mutate node operation hints.

## Node Scale-In Preparation Flow

```text
manager scale-in route
  -> management.App.MarkNodeLeaving / SetNodeDrainMode / NodeScaleInStatus / PlanNodeScaleIn / AdvanceNodeScaleIn / MarkNodeRemoved
  -> ControlSnapshotReader.LocalControlSnapshot
  -> RuntimeSummaryReader.NodeRuntimeSummary
  -> GatewayDrainWriter.SetNodeDrainMode for gateway drain writes
  -> SlotRuntimeStatusReader.SlotRuntimeStatus
  -> ChannelRuntimeMetaReader.ScanChannelRuntimeMetaSlotPage for status only
  -> SlotReplicaMoveWriter.RequestSlotReplicaMove for advance only
  -> NodeLifecycleWriter.MarkNodeRemoved for final safe removal only
  -> Stage 3 slot_replica_move task intent
```

The scale-in preparation routes are live under the manager node permission
surface. `MarkNodeLeaving` validates only the manager-facing node ID and
delegates the durable transition to the lifecycle writer. `SetNodeDrainMode`
validates that the target exists as a durable Data-role, non-Controller
`leaving` node before delegating gateway admission changes to the narrow
`GatewayDrainWriter`; it returns the fresh target runtime counters but does not
close existing sessions. The status, plan, and advance usecases require the
target node to exist in durable `leaving` state with the Data role, reject
controller-role targets, and fail closed when target health is missing, stale,
non-`alive`, or runtime-not-ready; when eligible active data replacement nodes
have missing, stale, non-`alive`, runtime-not-ready, or revision-stale health;
when runtime summaries are unknown or report missing or stale control
revisions; when there is live Slot leadership on the target, live Slot voters
that still contain the target after desired placement moved, Slot runtime read
failures, or active/failed Controller tasks that still reference the target.
Eligible active data replacement nodes do not use low-frequency health report
revision as the scale-in fence; the live runtime summary revision gate below
owns that safety check so health report cadence cannot falsely block drain
progress. After Slot/task checks, status performs a bounded scan of
authoritative `ChannelRuntimeMeta` by physical Slot and fails closed when the
target is still a Channel leader, configured replica, ISR member, or when
Channel inventory is unavailable. The inventory scan is bounded by both
per-page size and a total page budget; budget exhaustion is treated as unknown
inventory and keeps final removal unsafe. Status also reads the target runtime
summary as the final gateway drain gate: `safe_to_remove` stays false while gateway
drain mode is off, admission still accepts new sessions, or gateway/online/
closing/pending-activation counters are non-zero. Desired Slot peers containing
the target are reported as the Slot drain work remaining and are the only status
blocker that `PlanNodeScaleIn` may advance. `AdvanceNodeScaleIn` scans Slots in
stable Slot ID order, chooses the lowest health-schedulable replacement not
already in the peer set through `control.NodeSchedulableForPlacement`, and
submits bounded `slot_replica_move` intents through the existing Controller
writer. Unknown runtime, health, control revision, Slot runtime, or task data
keeps status unsafe and keeps planning or advancement at no candidates. Unknown
Channel inventory keeps final scale-in status unsafe, but it does not block
Slot drain advancement while desired Slot peers still contain the target.
Runtime drain blockers keep final status unsafe but do not block Slot drain
advancement. The final safety decision is a conjunction of the Controller
snapshot, health freshness, Slot runtime status, Controller task state, bounded
`ChannelRuntimeMeta` inventory, and target gateway/runtime drain counters.
After `NodeScaleInStatus` constructs a status response, the optional
`ScaleInStatusObserver` receives the bounded `blocked_reasons` list so app-level
metrics can aggregate blocker counts by reason only, with app-local
deduplication by node, control-state revision, and reason to keep status polling
from inflating counters. The observer hook does not own safety decisions and
does not add task ID, channel, UID, address, or node labels.
`MarkNodeRemoved` reuses the same status report and delegates to the lifecycle
writer with the status `StateRevision` only when `safe_to_remove=true`; unsafe
attempts fail closed before reaching the writer, and concurrent control-state
revision changes are reported as scale-in conflicts. Already-removed tombstones
are delegated to the same writer without the safety revision fence for
idempotent `changed=false` results, and lower ControllerV2 also preserves this
terminal-state idempotency when a retried remove still carries a stale fence.
It does not mutate
`DesiredPeers` directly, retry failed tasks, implement cancellation, close
gateway sessions, migrate Channel replicas, clear Channel metadata, or mark
nodes removed outside that safe final gate or idempotent tombstone path.

## Dynamic Node Diagnostics Flow

```text
manager diagnostics route
  -> management.App.DynamicNodeDiagnostics
  -> ControlSnapshotReader.LocalControlSnapshot
  -> existing scale-in/onboarding/task/audit/Slot projections
  -> bounded diagnostic DTO
```

Dynamic node diagnostics is read-only and uses one control snapshot as the
primary evidence source for node membership, Slot assignments, and active
Controller tasks. Leaving nodes reuse the scale-in status projection so
`safe_to_remove`, gateway drain counters, runtime unknowns, and
`blocked_reasons` match the removal gate. Active onboarding targets reuse the
onboarding status projection. The diagnostics response then attaches bounded
active task rows, retained task-audit snapshots, and Slot rows related to the
target node or its `slot_replica_move` tasks. The summary chooses a compact
`recommended_next_action` from the same evidence: inspect active Controller
tasks first, then task audits, Slot/runtime blockers, gateway drain blockers,
or removal readiness.

Task-audit and Slot-runtime readers are optional evidence sources. Read
failures are reported in `sources` and as bounded warnings rather than hidden
behind empty arrays; unknown runtime evidence keeps recommendations
conservative. The usecase never creates Controller tasks, mutates
`DesiredPeers`, retries failed work, or removes nodes from the diagnostics
path.

## Node Onboarding Flow

```text
manager HTTP handler
  -> management.App.PlanNodeOnboarding/StartNodeOnboarding/AdvanceNodeOnboarding/NodeOnboardingStatus
  -> ControlSnapshotReader.LocalControlSnapshot
  -> SlotReplicaMoveWriter.RequestSlotReplicaMove for start/advance only
  -> clusterv2 control slot_replica_move task intent
  -> clusterv2 task executor
  -> Slot Raft learner/config-change flow
  -> final ControllerV2 Slot assignment commit
```

The onboarding usecase implements only the first bounded Slot-replica
onboarding step. It requires the target to be a health-schedulable active data
node through `control.NodeSchedulableForPlacement`, caps `max_slot_moves` to
`1..5`, scans physical Slots in stable Slot ID order, chooses the source peer
from the highest projected replica count, skips Slots with any active Controller
task, and creates at most the requested number of `slot_replica_move` tasks. It
does not change `DesiredPeers` directly, run Slot Raft, poll task completion,
migrate Channel replicas, or implement operator cancellation without a fenced
Controller writer. Status is a read-only projection of active
`slot_replica_move` tasks targeting the selected node.
Targets being onboarded are task-local learners before promotion; the durable
`DesiredPeers` assignment changes only after the clusterv2 executor observes the
target voter set and commits the final ControllerV2 assignment update.

## Node Slot Move-Out Flow

```text
manager slot-move-out route
  -> management.App.PlanNodeSlotMoveOut/AdvanceNodeSlotMoveOut
  -> ControlSnapshotReader.LocalControlSnapshot
  -> SlotReplicaMoveWriter.RequestSlotReplicaMove for advance only
  -> clusterv2 control slot_replica_move task intent
```

Pure Slot move-out lets operators rebalance replicas away from an active Data
node without changing that node's lifecycle. The source node only needs to be
present, Data-role, and durable `active`; Controller-voter membership is not a
blocker because the operation does not mark the node `leaving`, toggle gateway
drain, or remove it. Planning scans physical Slots in stable Slot ID order,
skips Slots where the source is not in `DesiredPeers`, skips Slots with active
or failed blocking tasks, and chooses the lowest health-schedulable active Data
replacement not already in the peer set. Advance refreshes the control snapshot
per candidate and submits fenced `slot_replica_move` intents through the same
writer path as scale-in. It does not mutate `DesiredPeers` directly, run Slot
Raft, close sessions, migrate Channel replicas, mark the source node leaving,
or submit node removal.

## Slot List Flow

```text
manager HTTP handler
  -> management.App.ListSlots
  -> ControlSnapshotReader.LocalControlSnapshot
  -> clusterv2 control snapshot
  -> sorted manager Slot DTO rows
```

The Slot projection derives physical Slot assignments, preferred leaders,
config epochs, and logical hash-slot ownership from the local clusterv2 control
snapshot. The list uses desired peers as the fallback display runtime so the
web Slot list can always render quorum, sync, preferred leader, and voter
columns. `runtime.preferred_leader_id` is the control-plane preferred leader; it
is not the live Raft leader. When a positive `node_id` is selected and the Slot
Raft operator is wired, the usecase also performs a best-effort node-local or
routed peer status read for each returned Slot and attaches the selected node's
Raft leader, role, and commit/applied watermarks in `node_log`. Status read
misses are omitted from the row rather than failing the inventory response.
Active task summaries are derived from the same clusterv2 control snapshot and
attached to the matching Slot row. Slot leader-transfer requests use the same
control snapshot plus a live Slot runtime status reader to validate the current
leader, voter quorum, desired peers, and target-node active data lifecycle
before submitting a Controller-backed task intent. The usecase does not read
ControllerV2 state directly and only exposes this narrow task mutation route;
Slot detail, rebalance, recovery, and add/remove operation routes are outside
this migration step.

## Slot Leader Transfer Flow

```text
manager HTTP handler
  -> management.App.RequestSlotLeaderTransfer
  -> ControlSnapshotReader.LocalControlSnapshot
  -> SlotRuntimeStatusReader.SlotRuntimeStatus
  -> SlotLeaderTransferWriter.RequestSlotLeaderTransfer
  -> clusterv2 control leader-transfer task intent
```

The usecase rejects invalid IDs, missing Slot assignments, existing conflicting
tasks, targets outside desired peers, non-active or non-data target nodes, and
runtime voter sets that cannot prove quorum. Already-leader and same-task
requests are no-ops and do not require the writer port to be wired. New task
creation is delegated to the writer port with source leader, target leader,
target peers, config epoch, and observed control-state revision. Management
validation is intentionally limited to the current Slot Raft leader, voter set,
desired peers, and target-node active data lifecycle; it does not inspect target
health status, target match index, or predict whether Raft will ultimately
choose the preferred target.

## Slot Leader Transfer Batch Plan Flow

```text
manager HTTP handler
  -> management.App.PlanSlotLeaderTransfers
  -> ControlSnapshotReader.LocalControlSnapshot
  -> SlotRuntimeStatusReader.SlotRuntimeStatus
  -> ordered leader-transfer candidates, skipped rows, and deterministic plan_id
```

The batch planner is read-only. It scans the local clusterv2 control snapshot in
stable Slot order, reads live Slot runtime status for the scanned assignments,
and returns candidate rows, skip reasons, a summary, and a deterministic plan
ID fenced to the observed control-state revision. Planning may reuse matching
active leader-transfer tasks in the returned candidates, but it does not create
ControllerV2 tasks, mutate preferred leaders, or call Slot Raft.

## Slot Leader Transfer Batch Execute Flow

```text
manager HTTP handler
  -> management.App.ExecuteSlotLeaderTransferBatch
  -> management.App.PlanSlotLeaderTransfers
  -> state_revision + plan_id fence check
  -> SlotLeaderTransferWriter.RequestSlotLeaderTransfer for create candidates
  -> ordered per-Slot execute results
```

Batch execute re-runs the planner from the execute request, rejects stale
`state_revision` or mismatched `plan_id` before any writes, reports matching
pending or running existing candidates as no-ops, and submits create candidates
through the same Slot leader-transfer writer port. Matching failed
leader-transfer tasks are planned as create retries so the Controller task can
be reset to pending through the writer path. It does not call Slot Raft directly
and does not create a durable batch task.

## Controller Task Read Flow

```text
manager HTTP handler
  -> management.App.ListControllerTasks/ControllerTask
  -> ControlSnapshotReader.LocalControlSnapshot
  -> clusterv2 control snapshot Tasks
  -> sorted active Controller task DTO rows or one active task
```

The task read model exposes active ControllerV2 tasks only. Completed tasks are
absent because ControllerV2 removes them from active cluster state, while failed
tasks remain visible with status, attempt, task error, and participant progress.
List filtering is performed in the management usecase for kind, status,
physical Slot, and related node membership across source, target, target peers,
and participant rows. The usecase does not read Controller Raft logs, persist
history, mutate task state, or provide cancellation and retry operations.

## Distributed Log Flow

```text
manager HTTP handler
  -> management.App.ListControllerLogEntries/ListSlotLogEntries
  -> LogReader.ControllerLogEntries/SlotLogEntries
  -> node-local clusterv2 log read or node RPC routed peer read
  -> newest-first decoded manager log page
```

The management usecase validates `node_id`, `slot_id`, `limit`, and cursor
bounds, then delegates log storage selection to the `LogReader` port.
Controller and Slot log entries are read-only inspection summaries: the usecase
does not decode Raft payloads itself and does not expose any replay, truncation,
or mutation operation.

## Slot Raft Management Flow

```text
manager HTTP handler
  -> management.App.CompactSlotRaftLog
  -> SlotRaftOperator
  -> local clusterv2 node operation or node RPC routed peer operation
  -> one node-local Slot Raft status or compaction result
```

The node-scoped methods validate positive `node_id` and `slot_id` values and
delegate local-or-remote selection to the `SlotRaftOperator` port. Status reads
return the selected node's local Slot Raft role plus commit/applied watermarks.
Compaction returns a single node/Slot result that preserves success, skip
reason, and per-target error text. The usecase does not fan out to Slot
replicas and does not turn manual compaction into a replicated Raft command.

## Controller Raft Management Flow

```text
manager HTTP handler
  -> management.App.ControllerRaftStatus/CompactControllerRaftLog/CompactControllerRaftLogs
  -> ControllerRaftOperator
  -> local clusterv2 node operation or node RPC routed peer operation
  -> node-local Controller Raft status or compaction result
```

The node-scoped methods validate positive `node_id` values and delegate
local-or-remote selection to the `ControllerRaftOperator` port. The cluster-wide
manual compaction method reads the local control snapshot, selects nodes with
the Controller role, sorts them by node id, and fans out one explicit
node-local compaction attempt per Controller voter. Partial failures are
preserved in per-node results; the usecase does not retry, mask errors, or
turn compaction into a replicated Raft command.

## Application Log Flow

```text
manager HTTP handler
  -> management.App.ApplicationLogSources/ApplicationLogEntries
  -> ApplicationLogReader.ApplicationLogSources/ApplicationLogEntries
  -> selected-node ordinary WK_LOG_DIR application log source
  -> manager application log sources or entry page
```

Application logs are ordinary process logs written under the configured
`WK_LOG_DIR` by the selected node. They are distinct from Controller, Slot, and
Raft distributed logs: the management usecase does not treat them as replicated
state, does not decode consensus payloads, and does not resolve file names or
filesystem paths itself. It validates `node_id` and `limit` bounds, then
delegates source discovery, cursor handling, rotation detection, filtering, and
entry parsing to the narrow `ApplicationLogReader` port.

## Business Channel List Flow

```text
manager HTTP handler
  -> management.App.ListBusinessChannels
  -> local node_id: ControlSnapshotReader.LocalControlSnapshot
  -> remote node_id: RemoteBusinessChannelReader.NodeBusinessChannels
  -> ControlSnapshotReader.LocalControlSnapshot
  -> ChannelBusinessReader.ScanChannelsSlotPage
  -> Slot metadata channel rows
  -> filtered manager channel DTO rows
```

The business channel projection scans channel metadata by physical Slot,
filters out internal member-list and derived command channels, then applies the
manager `node_id`, `type`, `keyword`, `limit`, and cursor constraints. Empty
or local `node_id` requests scan this node's Slot metadata; non-local requests
delegate the whole page request to a narrow remote channel reader port. The read
model derives display `slot_id` and `hash_slot` values from the selected node's
clusterv2 control snapshot and keeps cursor state bound to the requested filter
values. Channel detail, member, and mutation operation routes are outside this
migration step.

## Channel Runtime Metadata Flow

```text
manager HTTP handler
  -> management.App.ListChannelRuntimeMeta
  -> ControlSnapshotReader.LocalControlSnapshot
  -> ChannelRuntimeMetaReader.ScanChannelRuntimeMetaSlotPage
  -> Slot-owned channel_runtime_meta rows
  -> node/channel filtered manager runtime DTO rows
```

The channel runtime projection scans authoritative runtime metadata by
physical Slot, applies optional `node_id`/`node_scope` and channel ID filters,
and returns legacy-compatible leader, replica, ISR, epoch, status, and optional
max-message-sequence fields. When the ChannelV2 migration store is wired, each
row also includes the active migration task ID, task-owned write-fence token,
write-fence version, bounded write-fence reason, and degraded hints such as
`min_isr_not_met` or `isr_below_replicas`. The `node_id` filter is a runtime
metadata membership filter, not a business channel metadata read. Generic
channel runtime mutations remain outside this migration step.

## Channel Migration Flow

```text
manager HTTP handler
  -> management.App.RequestChannelLeaderTransfer / RequestChannelReplicaReplace
  -> ChannelMigrationStore.CreateLeaderTransfer / CreateReplicaReplace
  -> clusterv2 ChannelV2 migration facade
  -> Slot-owned runtime metadata validation and migration task rows
```

Manual ChannelV2 migration requests are channel-scoped and use the Slot-owned
runtime metadata row as the source of truth. Leader-transfer requests require
the target to already be a replica. Replica-replacement requests require the
source to be a non-leader replica and the target to not already be a replica.
The store creates task-owned write fences and persists durable task rows through
Slot FSM commands; the management usecase only validates manager input shape,
normalizes channel IDs, maps duplicate active tasks or stale metadata to
`ErrChannelMigrationConflict`, maps missing tasks to
`ErrChannelMigrationNotFound`, and returns a compact task summary for HTTP.

Active, detail, and abort reads remain channel-scoped because tasks live in the
channel-owned hash slot. `ListActiveChannelMigrations` therefore requires the
channel identity and does not scan a global task table. `AbortChannelMigration`
first reads the selected channel task and then asks the migration store to mark
it aborted and clear the task-owned fence.

## Recent Conversation List Flow

```text
manager HTTP handler
  -> management.App.ListRecentConversations
  -> conversation.App.Sync
  -> UID conversation active view
  -> channel latest/recent message reads
  -> bounded manager recent conversation DTO rows
```

The recent conversation projection keeps legacy manager query bounds and
truncation behavior in the management usecase while delegating ordering,
unread calculation, and recent-message hydration to the internalv2 conversation
sync usecase. Embedded message timestamps are converted back to Unix seconds so
the manager JSON shape remains compatible with the existing web page.

## Message Management Flow

```text
manager HTTP handler
  -> management.App.ListMessages
  -> MessageReader.QueryMessages
  -> committed channel message log
  -> bounded manager message DTO rows

manager HTTP handler
  -> management.App.AdvanceMessageRetention
  -> MessageRetentionOperator.AdvanceMessageRetention
  -> manager retention outcome
```

Message list parsing, validation, cursor state, and response shaping stay in
the access and management layers; committed-log reads are delegated through a
narrow port. Message retention requests validate the legacy manager envelope
and delegate to an optional retention operator. The usecase owns no Slot,
ChannelV2, or storage details; the adapter decides whether the request can
advance a cluster-authoritative logical compaction boundary. When no retention
operator is wired, the usecase reports `ErrMessageRetentionUnavailable` so the
HTTP layer can return `503` instead of claiming a successful
delete-through-sequence operation.

## Connection Management Flow

```text
manager HTTP handler
  -> management.App.ListConnections/GetConnection
  -> owner-local online.Registry.LocalSessions when node_id is local or empty
  -> RemoteConnectionReader.NodeConnections/NodeConnection when node_id is remote
  -> manager connection DTO rows
```

The connection projection filters list results to active owner-local sessions,
maps the legacy manager DTO fields from `online.LocalSession`, applies a
default and maximum list limit of 100 rows, and sorts local list results by
newest connection first. Remote `node_id` filters delegate to a narrow
`RemoteConnectionReader` port with the same limit so the app layer can route
manager connection inventory reads over node RPC. When that port is not wired,
the usecase returns `ErrConnectionReaderUnavailable`.

## Plugin Management Flow

```text
manager HTTP handler
  -> management.App.ListNodePlugins/GetNodePlugin/UpdateNodePluginConfig/RestartNodePlugin/UninstallNodePlugin
  -> local node_id: PluginReader node-local lifecycle API
  -> remote node_id: RemotePluginReader manager plugin node RPC
  -> manager plugin DTO rows
```

Plugin management is node-scoped. It validates positive `node_id` values and
non-empty plugin numbers, reads the local v2 plugin usecase for the local node,
and delegates non-local reads and lifecycle mutations to a narrow
`RemotePluginReader` port. The projection clones hook method slices, config
template pointers, redacted desired config maps, desired-state timestamps, and
runtime fields such as status, sync flags, process ID, last-seen time, and
latest error text. Config update writes node-local desired state and preserves
secret redaction below the management layer; restart and uninstall delegate to
the selected node's plugin runtime through the plugin usecase. The management
usecase does not inspect plugin files or run plugin processes directly.

Plugin binding management is cluster-authoritative and UID-owned. The usecase
validates that list requests provide exactly one selector, trims mutation
identities, stamps accepted binds with `Now`, and delegates persistence to a
`PluginBindingStore` port backed by Slot metadata. UID reads use
`ListPluginBindingsByUID`; plugin-centric pages are used only when the store
also exposes `PluginBindingPluginScanner`, so internalv2 does not fake a
cluster-wide reverse scan from one local node. When the node-local plugin
runtime is wired, binding rows include non-fatal warnings for missing,
disabled, non-running, or Receive-unsupported plugins; binding persistence does
not depend on local plugin runtime availability.

## DB Inspect Flow

```text
manager HTTP handler
  -> management.App.ListDBInspectTables/DescribeDBInspectTable/QueryDBInspect
  -> empty node_id or local node_id: DBInspectReader node-local read
  -> non-local node_id: RemoteDBInspectReader manager DB inspect node RPC
  -> read-only DB Inspect DTO rows
```

DB Inspect is a read-only, node-local diagnostics surface for manager
inspection. The management usecase normalizes an empty `node_id` to the local
manager node, validates requested target nodes against the cluster snapshot,
and routes non-local `node_id` requests through the narrow remote DB inspect
reader port. It does not merge rows across cluster nodes, accept or expose
filesystem paths, or mutate storage; storage selection is owned by the app
composition root and the underlying inspect reader.

## Diagnostics Flow

```text
manager HTTP handler
  -> management.App.QueryDiagnostics/CreateDiagnosticsTrackingRule/ListDiagnosticsTrackingRules/DeleteDiagnosticsTrackingRule
  -> local node_id or aggregate target: DiagnosticsReader node-local query
  -> non-local node_id or fan-out target: DiagnosticsReader manager diagnostics node RPC
  -> internalv2 diagnostics query result or tracking-rule mutation result
```

Diagnostics queries normalize trace, message, and event filters in the
management usecase, select alive or suspect nodes from the clusterv2 control
snapshot for aggregate reads, and skip down nodes with per-node notes instead
of masking the cluster shape. Tracking-rule mutations fan out to eligible
nodes and preserve per-node successes, skips, and errors. The usecase depends
only on the internalv2 diagnostics DTOs and the narrow diagnostics ports.

## User Management Flow

```text
manager HTTP handler
  -> management.App.ListUsers/GetUser/KickUser/ResetUserToken
  -> ControlSnapshotReader.LocalControlSnapshot
  -> UserReader.ScanUsersSlotPage/GetUser/GetDevice
  -> UserPresenceDirectory.EndpointsByUIDs
  -> UserOperator.UpdateToken/DeviceQuit
  -> optional UserRouteActionDispatcher.ApplyRouteAction
```

The user list and detail projections scan UID metadata by physical Slot and
join stored device-token rows with authoritative presence routes. Manager
`keyword`, `limit`, and cursor constraints stay in this package so HTTP remains
an adapter. Force-offline and token-reset actions reuse the internalv2 user
usecase; when a route-action dispatcher is available, force-offline also asks
the owner node for matching active routes to close.

System UID manager actions normalize, deduplicate, and sort UID values around
the internalv2 user usecase's persisted system UID list.
