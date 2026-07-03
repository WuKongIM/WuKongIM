# internal/usecase/message Flow

## Responsibility

`internal/usecase/message` owns the entry-agnostic message facade,
legacy-compatible send permission checks, and compatible channel message sync.
Allowed SEND work is delegated to a configured `channelappend.Submitter`; this
package does not own channel authority routing, durable append, message ID
allocation, or post-commit delivery effects. It knows about permission metadata
ports and the channel message read port for sync, but not gateway frames, wire
protocols, HTTP JSON, or concrete cluster runtimes.

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
```

The sync usecase returns `SyncedMessage` DTOs with the fields needed by legacy
HTTP responses. Concrete storage adapters may return zero values for fields that
the current ChannelV2 write path does not persist yet.

## Import Boundary

The usecase package must remain independent from concrete entries and cluster
adapters. The import-boundary test rejects imports of:

- `pkg/gateway`
- `pkg/protocol/frame`
- `pkg/cluster`
- `pkg/channelv2`
- `internal/access`
- `internal/app`
