# Send Permission Convergence P2 Design

## Context

`docs/raw/send-path-business-logic-diff.md` records that P0/P1/P2a/P2b/P2c restored the core send path for person/group sends, channel status checks, no-persist, command-channel writes, and request-scoped subscribers. The remaining permission-related gaps against `learn_project/WuKongIM` are narrower but security-sensitive:

- person receiver allowlist is still not enforced when an operator wants legacy personal whitelist mode;
- receiver system accounts should not block person sends through receiver-side deny/allow checks;
- gateway sends do not carry the device ID into `message.Send`, so legacy system-device bypass cannot be represented;
- `message.Send` still rejects legacy public/special channel types before permission can apply.

This spec keeps business rules in `internal/usecase/message`, keeps `internal/access/*` as adapters, and does not move authorization into `pkg/channel`.

## Goals

1. Restore the remaining send-permission semantics that can be expressed with the current usecase/runtime model.
2. Keep the default behavior safe and compatible: person allowlist remains off by default, matching old `WhitelistOffOfPerson=true`.
3. Preserve cluster-only semantics: every durable send still goes through the channel log/cluster path; single-node deployment remains a single-node cluster.
4. Make special channel support explicit and small: only open types whose subscriber resolution already exists in `internal/usecase/delivery`.

## Non-goals

- Do not implement plugin/webhook/AI hooks.
- Do not implement `sendbatch`, offline CMD conversation sync, `systemcmdonline`, or `expire` behavior.
- Do not add business authorization to `pkg/channel` append or ISR code.
- Do not change request-scoped subscriber persistence/replay semantics.

## Business Rules

### Supported Send Channel Types

`message.App.Send` should accept these external send types:

- `Person`
- `Group`
- `CustomerService`
- `Info`
- `Visitors`
- `Agent`

`Temp` remains internal to request-scoped subscriber sends and command derivation. Other types still return `ReasonNotSupportChannelType` until a later dedicated design covers their business semantics.

### Channel ID Normalization

- Command suffix handling remains first: if the incoming channel ID ends with `____cmd`, permission uses the original channel ID and durable `SyncOnce` writes back to the command channel.
- If the incoming channel ID already ends with `____cmd`, durable append must still target that command channel even when `SyncOnce=false`; the usecase must not drop the command suffix and must not double-append it.
- `Person` keeps current normalization: a direct receiver UID becomes the canonical fake person channel; an invalid fake channel errors.
- `Agent` gains legacy-compatible normalization: if the channel ID is a bare agent UID, the usecase converts it to `fromUID@agentUID`; if it already contains `@`, the usecase validates the two-part shape and uses it as-is.
- `Info`, `CustomerService`, `Visitors`, and `Group` keep the incoming channel ID.

### Bypass Order

`checkSendPermission` should keep this order:

1. If no permission store exists, allow. This preserves existing unit-test and minimal wiring behavior.
2. If sender is a system UID, allow. This preserves the already-restored P0 system-UID bypass behavior.
3. Check sender personal `SendBan`; missing sender channel metadata means not send-banned.
4. If a configured system device ID is non-empty and the gateway-sourced `SendCommand.DeviceID` matches it, allow the remaining channel-type-specific permission. A system device must not bypass sender `SendBan`.
5. Apply channel-type-specific permission.

### Person Channel Permission

After normalization, derive the receiver from the canonical person channel.

- If the receiver is a system UID, allow. This mirrors legacy behavior where sending to a system account bypasses receiver-side allow/deny checks.
- Check receiver-dimensional denylist: `deny/person/<receiver>` contains sender -> `ReasonInBlacklist`.
- If `PersonWhitelistEnabled` is false, allow after denylist miss.
- If `PersonWhitelistEnabled` is true, require receiver-dimensional allowlist membership: `allow/person/<receiver>` contains sender. Missing membership returns `ReasonNotInWhitelist`.

### Group/Common Channel Permission

Group keeps the already-restored strict sequence:

1. Channel metadata must exist; missing metadata returns `ReasonChannelNotExist`.
2. `Ban` returns `ReasonBan`.
3. `Disband` returns `ReasonDisband`.
4. Denylist membership returns `ReasonInBlacklist`.
5. Missing subscriber membership returns `ReasonSubscriberNotExist`.
6. If allowlist exists and sender is absent, return `ReasonNotInWhitelist`.
7. Otherwise allow.

### Public/Special Channel Permission

- `Info`: after sender `SendBan` and system bypasses, allow. Channel `Ban`/`Disband`, subscriber, denylist, and allowlist are not checked, matching legacy public-channel permission.
- `CustomerService`: after sender `SendBan` and system bypasses, allow.
- `Agent`: after sender `SendBan` and system bypasses, allow only when `FromUID` is either side of the normalized `uid@agentUID` channel. Otherwise return `ReasonNotAllowSend`. This is intentionally stricter than legacy fallthrough to a generic subscriber check because the current delivery resolver derives agent recipients from the two channel IDs and does not use a third-party subscriber list for agent sends.
- `Visitors`: after sender `SendBan` and system bypasses, allow when `FromUID == ChannelID` because the visitor channel ID is the visitor UID. Other senders use the same denylist/subscriber/allowlist sequence as a common channel without requiring channel metadata, but the lookup key must be the customer-service subscriber source: `channel_id=<visitor channel ID>`, `channel_type=CustomerService`. This matches delivery's `Visitors` overlay, where the visitor UID is overlaid and customer-service subscribers are read from the customer-service channel. Permission denials still use the same common-channel reason codes while public-channel `Ban`/`Disband` checks remain bypassed.

## Configuration

Add a small message config section:

- `Message.PersonWhitelistEnabled` / `WK_MESSAGE_PERSON_WHITELIST_ENABLED`, default `false`.
  - English comment must explain that this restores legacy receiver-side personal allowlist enforcement and is disabled by default for compatibility.
- `Message.SystemDeviceID` / `WK_MESSAGE_SYSTEM_DEVICE_ID`, default `____device`.
  - English comment must explain that matching gateway device IDs bypass send business permissions for legacy system-message compatibility.

Update `wukongim.conf.example` with both keys and comments.

## Component Changes

- `internal/runtime/channelid`: add `EncodeAgentChannel` / `DecodeAgentChannel` helpers with tests.
- `internal/usecase/message`:
  - add `SendCommand.DeviceID` and `SendCommand.DeviceFlag` fields;
  - add message options for person whitelist and system device ID;
  - update `Send` supported-type gate and agent normalization;
  - split common-channel member checks so group can retain channel-state checks while visitors can reuse membership checks without metadata;
  - enforce receiver system UID and optional person allowlist.
- `internal/access/gateway`: map session `DeviceID` / `DeviceFlag` into `SendCommand`.
- `DeviceFlag` is intentionally pass-through only in this slice; no send permission rule consumes it yet, but carrying it keeps the command shape aligned with gateway session identity for future device-aware rules.
- `internal/app` and `cmd/wukongim`: parse and wire message config.

## Error Handling

- Permission denials remain normal `SendResult.Reason` values and do not enter durable append.
- Infrastructure/store failures return `ReasonSystemError` with the underlying error.
- Invalid agent channel IDs use the same error-handling style as invalid person channel IDs. Existing access-layer error mapping may be extended only if necessary.

## Testing

Use focused unit tests rather than integration tests for this slice:

- message usecase tests for person allowlist default-off, enabled rejection, enabled success, receiver system UID bypass, system device bypass, public/special channel support, and durable append prevention on rejection;
- visitors fallback tests proving non-visitor senders read denylist/subscriber/allowlist from `(visitorChannelID, CustomerService)`;
- command-channel tests proving an already-`____cmd` input appends to the command channel even when `SyncOnce=false`;
- gateway mapper/handler tests proving device ID and flag flow into `SendCommand`;
- config tests for `WK_MESSAGE_PERSON_WHITELIST_ENABLED`, `WK_MESSAGE_SYSTEM_DEVICE_ID`, and environment override behavior;
- channel ID helper tests for agent encode/decode.

Run focused verification first:

```bash
GOWORK=off go test ./internal/runtime/channelid ./internal/usecase/message ./internal/access/gateway ./internal/app ./cmd/wukongim -count=1
```

Run broader unit tests only if the focused set exposes cross-package regressions.
