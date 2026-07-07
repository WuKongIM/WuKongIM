# internal/usecase/message Flow

## Responsibility

`internal/usecase/message` owns the entry-agnostic message facade,
legacy-compatible send permission checks, and compatible channel message sync.
Allowed SEND work is delegated to a configured `channelappend.Submitter`; this
package does not own channel authority routing, durable append, message ID
allocation, or post-commit delivery effects. It knows about permission metadata
ports, the channel message read port for sync, and the message event projection
store port, but not gateway frames, wire protocols, HTTP JSON, or concrete
cluster runtimes.

## SendBatch Flow

```text
SendBatch(items)
  -> for each item:
       normalize command-channel IDs to their source channel for permission checks
       normalize person-channel IDs when requested by the entry adapter
       if PermissionStore is nil, allow
       if sender is a system UID, allow
       check sender SendBan through the sender's person metadata row
       if DeviceID matches SystemDeviceID, allow channel-specific checks
       enforce group metadata, ban/disband, subscriber, denylist, and allowlist checks
       enforce person receiver denylist and optional receiver allowlist/AllowStranger checks
       enforce agent participant and visitors/customer-service membership checks
       reject denied items with item-aligned Reason values
       if SendHook is configured, run it before append admission
       reject hook-denied items with item-aligned Reason values
  -> if Submitter is nil, return ErrRouteNotReady for remaining allowed items
  -> delegate allowed items to Submitter.SendBatch
  -> copy delegated results back to original item indexes
```

`Send(ctx, cmd)` runs the same permission check and delegates allowed commands
directly to `Submitter.Send`. When configured, `SendHook.BeforeSend` runs after
permission success and before the submitter. It may mutate the command payload
or reject with a usecase `Reason`; it does not run for permission-rejected
items. Plugin-origin sends carry `Origin`/`HookDepth` recursion controls, and
trusted internal paths may set `SkipPluginHooks`.

The configured submitter is normally the app-level channel append router, which
resolves channel append authority and admits work into the authority node's
channel append reactor. Validation, request-scoped command-channel derivation,
message ID allocation, append retries, committed cursors, subscriber scan,
conversation projection, NoPersist realtime dispatch, PersistAfter hooks, and
online delivery are all owned by `internal/runtime/channelappend`.

Permission checks preserve legacy reason semantics while staying
entry-agnostic: `internal/access/gateway` and `internal/access/api` map the
usecase `Reason` values back to protocol reason codes at their boundaries.
`PermissionCacheTTL` optionally wraps the permission metadata port with a
bounded read-through cache for channel rows, subscriber point lookups,
subscriber-set non-emptiness, and missing channel rows.

## SyncChannelMessages Flow

```text
SyncChannelMessages(query)
  -> validate login_uid, channel_id, and channel_type with legacy error strings
  -> canonicalize person-channel IDs using login_uid
  -> cap limit to the legacy maximum
  -> call ChannelMessageReader.SyncMessages with a normalized ChannelID
  -> treat missing channel runtime/storage as an empty page
  -> clone payloads before returning SyncedMessage values to access adapters
  -> when event_summary_mode is set, or include_event_meta asks for default
     full metadata, batch-read MessageEventStore states for stream messages by
     (channel_id, channel_type, client_msg_no) and attach compact event_meta
```

The sync usecase returns `SyncedMessage` DTOs with the fields needed by legacy
HTTP responses. Concrete storage adapters may return zero values for fields that
the current Channel write path does not persist yet. Message event enrichment is
page-batched for messages carrying the legacy stream setting bit and capped per
message so a high-limit sync request does not issue event-state reads for
ordinary messages or return unbounded lane state.

## AppendMessageEvent Flow

```text
AppendMessageEvent(event)
  -> validate required channel_id, channel_type, client_msg_no, event_id, event_type
  -> trim fields, lower-case event_type, default empty event_key to "main"
  -> force stream.finish onto the reserved finish event key
  -> canonicalize person-channel and agent-channel IDs using from_uid
  -> stamp updated_at when absent
  -> call MessageEventStore.AppendMessageEvent
  -> return the projected lane status and the assigned msg_event_seq when durable
```

`AppendMessageEvent` leaves stream buffering policy to the configured
`MessageEventStore`. The cluster-backed store keeps `stream.open`,
`stream.delta`, and `stream.snapshot` updates in a bounded Slot-leader cache and
only proposes a durable projection when a terminal event
(`stream.close`/`stream.error`/`stream.cancel`/`stream.finish`) arrives. Cache
hits may return `msg_event_seq=0` because no Slot FSM cursor has advanced yet;
terminal responses return the durable reducer-assigned message event sequence.
When `stream.finish` completes a message-level stream, the cluster store flushes
all still-open cached event lanes and the reserved finish marker in one Slot FSM
batch proposal. If Slot leadership changes before finish and the new leader has
no cached lanes, the cluster store fails the finish closed instead of writing a
completed projection that silently drops cache-only deltas; callers must replay
the stream deltas or provide a complete finish snapshot before retrying finish.
Cache pressure is reported as typed backpressure rather than silently evicting
active streams.

This phase treats `(channel_id, channel_type, client_msg_no)` as the projection
anchor and does not perform a routed base-message existence check before
appending event state. A future anchor policy should use a cluster-routed
message lookup rather than a node-local idempotency index so `/message/event`
does not depend on which node accepted the request. Empty `from_uid` is also
kept as an ordinary empty sender at this usecase boundary; any system-UID
defaulting must be owned by the access/app configuration layer.

## Message Event Projection Ports

`MessageEventStore` is the usecase boundary for message-scoped event
projections. It accepts one event update, may satisfy in-flight stream updates
from cache, returns the assigned message-level `msg_event_seq` once durable, and
reads compact event lane states in batch for `/channel/messagesync` enrichment.
The usecase DTOs are independent from the concrete Slot/metadb storage types;
cluster adapters perform mapping and payload cloning. Fine-grained
`/message/eventsync` replay is intentionally not part of this port in this
phase.

## Import Boundary

The usecase package must remain independent from concrete entries and cluster
adapters. The import-boundary test rejects imports of:

- `pkg/gateway`
- `pkg/protocol/frame`
- `pkg/cluster`
- `pkg/channel`
- `internal/access`
- `internal/app`
