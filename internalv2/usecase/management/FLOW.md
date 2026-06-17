# internalv2/usecase/management Flow

## Responsibility

`internalv2/usecase/management` builds entry-independent read models for the
new manager API. It currently owns the node list, Slot list, business channel
list, channel runtime metadata list, Controller/Slot distributed log pages,
recent conversation list, channel message list, message retention adapter
contract, local-or-remote connection list/detail projection, user management,
and system UID projections/actions used by `GET /manager/nodes`,
`GET /manager/slots`, `GET /manager/channels`,
`GET /manager/channel-runtime-meta`, `GET /manager/controller/logs`,
`GET /manager/slots/:slot_id/logs`, `GET /manager/conversations`, `GET /manager/messages`,
`POST /manager/messages/retention`, `/manager/connections*`,
`/manager/users*`, and `/manager/system-users*`.

## Node List Flow

```text
manager HTTP handler
  -> management.App.ListNodes
  -> ControlSnapshotReader.LocalControlSnapshot
  -> clusterv2 control snapshot
  -> sorted manager node DTO rows
```

The projection derives node identity, health, controller role, and lightweight
Slot placement counts from the local clusterv2 control snapshot. Runtime online
counters are marked unknown until a migrated runtime summary source exists.
Node operation action hints are always false because lifecycle, scale-in, and
onboarding operation routes are intentionally outside this migration step.

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
snapshot. Until a migrated Slot runtime observation source exists, the list
uses desired peers as the best available display runtime so the web Slot list
can render quorum, sync, leader, and voter columns. Slot detail, rebalance,
recovery, leader-transfer, and add/remove operation routes are outside this
migration step.

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
max-message-sequence fields. The `node_id` filter is a runtime metadata
membership filter, not a business channel metadata read. Channel replica
operations and runtime mutations remain outside this migration step.

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
```

Message list parsing, validation, cursor state, and response shaping stay in
the access and management layers; committed-log reads are delegated through a
narrow port. Message retention requests validate the legacy manager envelope
and delegate to an optional retention operator. When no retention operator is
wired, the usecase reports `ErrMessageRetentionUnavailable` so the HTTP layer
can return `503` instead of claiming a successful delete-through-sequence
operation.

## Connection Management Flow

```text
manager HTTP handler
  -> management.App.ListConnections/GetConnection
  -> owner-local online.Registry.LocalSessions when node_id is local or empty
  -> RemoteConnectionReader.NodeConnections/NodeConnection when node_id is remote
  -> manager connection DTO rows
```

The connection projection filters list results to active owner-local sessions,
maps the legacy manager DTO fields from `online.LocalSession`, and sorts local
list results by newest connection first. Remote `node_id` filters delegate to a
narrow `RemoteConnectionReader` port so the app layer can route manager
connection inventory reads over node RPC. When that port is not wired, the
usecase returns `ErrConnectionReaderUnavailable`.

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
