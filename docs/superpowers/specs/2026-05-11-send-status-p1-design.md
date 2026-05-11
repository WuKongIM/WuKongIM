# Send Status P1 Design

## Overview

P1 restores two legacy send-before-append status permissions that are still missing after P0:

- Sender global `SendBan`: if the sender's person-channel metadata has `SendBan=1`, reject any user send with `frame.ReasonSendBan`.
- Channel `Disband`: if the target group/channel metadata has `Disband=1`, reject group sends with `frame.ReasonDisband`.

Both checks must happen in `internal/usecase/message` before durable channel-log append, matching the current architecture where access layers only adapt protocols and `pkg/channel` owns log consistency, not business authorization.

## Goals

- Persist `Disband` and `SendBan` in slot channel metadata.
- Preserve channel metadata flags across Raft FSM commands, slot proxy channel RPC, local reads, and authoritative remote reads.
- Make channel management `/channel/info` and channel usecase updates persist `Ban`, `Disband`, and `SendBan` together.
- Reject sends before durable append for:
  - sender global `SendBan` -> `frame.ReasonSendBan`
  - target group/channel `Disband` -> `frame.ReasonDisband`
- Keep the P0 system UID bypass ahead of all business permission checks.

## Non-Goals

- Do not implement `AllowStranger`, `Large`, `NoPersist`, `SyncOnce`, cmd-channel conversion, request-scoped subscribers, `sendbatch`, plugin/webhook/AI hooks, or stream-message legacy behavior.
- Do not move business checks into gateway, HTTP API, or `pkg/channel/handler/append.go`.
- Do not add a deployment branch that bypasses cluster semantics; single-node deployment remains a single-node cluster.

## Legacy Behavior Reference

Legacy `learn_project/WuKongIM` performs these checks before channel persistence:

- `internal/user/handler/event_onsend.go` checks the sender person-channel `SendBan` and returns `ReasonSendBan`.
- `internal/service/permission.go` checks target channel `Ban` then `Disband` and returns `ReasonBan` or `ReasonDisband`.

P1 keeps the same denial reasons while fitting the new layered architecture.

## Architecture

### Metadata Model

Extend `pkg/slot/meta.Channel` with durable fields:

- `Disband int64`: channel is dissolved and cannot accept normal sends.
- `SendBan int64`: channel/user cannot send while receive semantics remain independent.

`Ban` remains unchanged. `SubscriberMutationVersion` remains the version fence for subscriber mutations and must not be reset by status updates.

### Slot Metadata Storage

Extend the channel family value codec with optional columns for `Disband` and `SendBan`. Existing stored rows may only contain `Ban` and `SubscriberMutationVersion`; decoding must treat missing `Disband` and `SendBan` as zero. This keeps old local test data and rolling internal data readable.

The channel ID index can remain keyed by `Ban` only, because no current query filters by `Disband` or `SendBan`. Authoritative point reads must use the primary family value, so they will return all flags.

### Raft FSM

Extend the `UpsertChannel` TLV command with new channel tags for `Disband` and `SendBan`. The command decoder already skips unknown tags, so adding fields preserves forward-compatible parsing. New encoders must emit all supported status flags.

### Slot Proxy RPC

Extend channel RPC response encoding by appending `Disband` and `SendBan` after existing channel fields. This RPC is internal to same-version nodes during this P1 branch; tests should cover round-trip behavior. The request format does not need to change.

### Channel Usecase

Prefer a richer store method over another positional boolean argument:

```go
UpsertChannel(ctx context.Context, ch metadb.Channel) error
```

`internal/usecase/channel.App.UpdateInfo` should build a `metadb.Channel` from `Info` and persist `Ban`, `Disband`, and `SendBan`. Existing `UpdateChannel(ctx, channelID, channelType, ban)` can remain where useful for compatibility, but the usecase should call the richer method to avoid dropping fields.

### Message Permission Flow

`message.App.Send` already calls `checkSendPermission` after person-channel normalization and before durable append. P1 extends that checker:

1. If sender is a system UID, return success immediately.
2. Check sender global send status by reading person-channel metadata for `cmd.FromUID` and `frame.ChannelTypePerson`.
   - If not found, treat as no `SendBan`; old deployments may not create a person-channel metadata row for every user.
   - If `SendBan != 0`, return `frame.ReasonSendBan`.
   - If another error occurs, return `frame.ReasonSystemError` with the error.
3. For group sends, read target channel metadata as P0 already does.
   - If not found, return `frame.ReasonChannelNotExist`.
   - If `Ban != 0`, return `frame.ReasonBan`.
   - If `Disband != 0`, return `frame.ReasonDisband`.
   - Continue denylist, subscriber, and allowlist checks in the existing P0 order.
4. For person sends, keep the existing receiver denylist check. Do not add `AllowStranger` or receiver-disband semantics in P1.

## Error Handling

Permission denials return `SendResult{Reason: reason}, nil`, preserving the current sendack/API reason behavior. Infrastructure or storage failures return `ReasonSystemError` plus an error so callers can distinguish operational failure from business denial.

## Testing Strategy

- `pkg/slot/meta`: verify `Disband` and `SendBan` persist, update, and decode missing legacy columns as zero.
- `pkg/slot/fsm`: verify `UpsertChannel` command round-trips both new fields.
- `pkg/slot/proxy`: verify channel RPC binary codec and authoritative reads preserve both fields.
- `internal/usecase/channel`: verify `UpdateInfo` persists `Ban`, `Disband`, and `SendBan`.
- `internal/usecase/message`: verify send rejects sender `SendBan`, rejects group `Disband`, and system UID bypass still skips both.
- Optional API-level coverage: verify `/channel/info` request wiring passes status flags into the channel usecase if existing test seams make this cheap.

## Rollout Notes

- No config changes are required.
- No gateway or HTTP request shape changes are required because `/channel/info` already accepts these fields.
- Update `pkg/slot/FLOW.md` if the channel metadata persistence/RPC description becomes stale.
- Update `docs/raw/send-path-business-logic-diff.md` after implementation to mark `SendBan` and `Disband` as restored in current P1.
