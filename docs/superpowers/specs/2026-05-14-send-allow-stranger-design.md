# Send AllowStranger Permission Design

## Context

The send permission convergence work has restored sender `SendBan`, channel `Ban` / `Disband`, group denylist / subscriber / allowlist checks, receiver-side person denylist, optional person allowlist enforcement, system UID bypasses, and system-device bypasses.

`AllowStranger` remains a documented legacy channel metadata flag:

- old API models expose `allow_stranger` on channel info;
- old `wkdb.ChannelInfo` stores and indexes `AllowStranger`;
- old docs describe it as a person-channel flag that lets strangers send to the receiver.

The current code accepts `allow_stranger` in the HTTP channel API and carries `channel.Info.AllowStranger`, but `internal/usecase/channel.App.UpdateInfo` drops it before persisting slot metadata. `pkg/slot/meta.Channel`, slot FSM commands, and slot proxy channel metadata RPCs do not carry it. `internal/usecase/message.checkPersonSendPermission` therefore cannot observe it and only supports denylist plus optional receiver allowlist.

The old `learn_project/WuKongIM/internal/service/permission.go` tree does not visibly check `AllowStranger` in `allowSend`; it checks receiver denylist and, when `WhitelistOffOfPerson=false`, receiver allowlist. Because legacy models, docs, changelog, and storage include `AllowStranger`, this design restores the intended business semantic while preserving the current project's explicit compatibility gate: `Message.PersonWhitelistEnabled`.

## Goals

1. Persist `AllowStranger` as authoritative channel metadata through slot meta, slot FSM, slot proxy, and channel usecase APIs.
2. Restore person-send permission semantics for receiver-owned `AllowStranger` without changing default open person-send behavior.
3. Keep denylist and sender `SendBan` precedence unchanged.
4. Keep business authorization in `internal/usecase/message`; access layers remain protocol adapters and `pkg/channel` remains data-plane oriented.
5. Preserve cluster-only semantics: the permission read still goes through the authoritative slot store, including single-node cluster deployments.

## Non-goals

- Do not add a new global config knob beyond the existing `Message.PersonWhitelistEnabled`.
- Do not implement plugin, webhook, AI, `expire`, sendbatch, temp-channel, or online-CMD behavior.
- Do not make `AllowStranger` affect group, customer-service, visitors, agent, info, or command-channel semantics.
- Do not add an independent non-cluster metadata store or a single-node shortcut.
- Do not reinterpret existing allowlist / denylist storage keys.

## Business Rules

`AllowStranger` is a receiver-owned personal channel flag. It is read from the receiver's person channel metadata: `(channel_id=<receiver UID>, channel_type=Person)`.

The effective person permission order should be:

1. If no permission store exists, allow. This preserves existing minimal wiring behavior.
2. If the sender UID is a configured system UID, allow.
3. Check sender personal metadata `(from_uid, Person)` for `SendBan`; if set, return `ReasonSendBan`. Missing sender metadata means not send-banned.
4. If the gateway-sourced device ID matches `Message.SystemDeviceID`, allow the remaining receiver-side permission. System device must not bypass sender `SendBan`.
5. Normalize the person channel ID and derive the receiver UID using the existing person-channel helper path already used by `checkPersonSendPermission`; do not introduce a second normalization rule.
6. If the receiver UID is a configured system UID, allow.
7. Check receiver-dimensional denylist `deny/person/<receiver>`; if sender is present, return `ReasonInBlacklist`.
8. If `PersonWhitelistEnabled=false`, allow. `AllowStranger=false` must not become a new default rejection path.
9. If `PersonWhitelistEnabled=true`, check receiver-dimensional allowlist `allow/person/<receiver>`:
   - if sender is allowlisted, allow;
   - otherwise, if receiver metadata has `AllowStranger != 0`, allow;
   - otherwise return `ReasonNotInWhitelist`.

This means `AllowStranger` is a compatibility escape hatch for deployments that enable strict personal allowlists. It expands who may send; it never tightens default behavior by itself.

## Metadata Semantics

Add `AllowStranger int64` to `pkg/slot/meta.Channel`.

Rules:

- `0` means disabled or absent.
- non-zero means enabled.
- only person-channel permission consumes the flag.
- old records without the field decode as `0`.
- channel update APIs continue to use full flag replacement semantics: if `allow_stranger` is omitted from a legacy JSON request, it is treated as false, matching the current request model for other flags.

## Component Design

### Slot Meta

`pkg/slot/meta` should add an `allow_stranger` column to the channel table as the next channel column ID after `send_ban`.

The primary channel family should encode and decode `AllowStranger` alongside `Ban`, `Disband`, `SendBan`, and `SubscriberMutationVersion`. The existing value encoding is field-oriented, so missing `allow_stranger` in older records should naturally default to zero.

Affected tests should prove create, upsert, get, scan, and snapshot/testutil encoding preserve `AllowStranger`.

### Slot FSM

`pkg/slot/fsm` upsert-channel commands should include an optional `AllowStranger` TLV field.

Rules:

- new encoders write the field;
- decoders default missing field to zero;
- unknown-tag skipping remains unchanged;
- command inspection should include `allow_stranger` so tests and diagnostics show the full business metadata.

### Slot Proxy

`pkg/slot/proxy` channel metadata RPCs should carry `AllowStranger` in `metadb.Channel` payloads.

To keep older test fixtures and local binary decoding robust, append `AllowStranger` after the existing `SubscriberMutationVersion` field and make the reader default it to zero when no bytes remain. This is not a rolling-upgrade protocol guarantee, but it avoids making old encoded in-process fixtures corrupt.

`GetChannelForPermission` must return the authoritative slot owner's `AllowStranger` value because message permission checks depend on it.

### Channel Usecase And APIs

`internal/usecase/channel.App.UpdateInfo` should map `channel.Info.AllowStranger` into `metadb.Channel.AllowStranger`.

`channel.Info.AllowStranger`'s comment should change from compatibility-only to the restored semantic: it permits stranger sends to person channels when personal whitelist enforcement is enabled.

The existing `internal/access/api` legacy channel endpoints already parse `allow_stranger` and should require only assertion updates. No access-layer authorization logic should be added.

### Message Usecase

`internal/usecase/message.checkPersonSendPermission` should read receiver personal channel metadata only after denylist misses and only when `PersonWhitelistEnabled=true` needs to decide whether a non-allowlisted sender can still send.

Receiver metadata lookup rules:

- `ErrNotFound` means `AllowStranger=false`.
- other errors return `ReasonSystemError`.
- metadata lookup should be skipped when sender is already allowlisted.

This keeps the hot path minimal for default deployments where `PersonWhitelistEnabled=false`.

## Error Handling

- Permission denials continue to return `SendResult.Reason`; they do not append durable messages.
- Store failures while reading `AllowStranger` return `ReasonSystemError` with the underlying error.
- Missing receiver metadata is not an infrastructure failure; it means no `AllowStranger` override is configured.
- Invalid person channel IDs keep the existing error behavior.

## Testing

Use TDD for implementation: add failing tests first, watch them fail for the missing behavior, then implement.

Focused tests:

- `internal/usecase/message`:
  - `PersonWhitelistEnabled=true`, sender not in receiver allowlist, receiver `AllowStranger=1` -> send succeeds;
  - receiver denylist still returns `ReasonInBlacklist` even when `AllowStranger=1`;
  - sender `SendBan` still returns `ReasonSendBan` before `AllowStranger` can matter;
  - `PersonWhitelistEnabled=false`, receiver `AllowStranger=0` remains allowed after denylist miss;
  - receiver metadata missing while checking `AllowStranger` returns `ReasonNotInWhitelist`;
  - receiver metadata store error while checking `AllowStranger` returns `ReasonSystemError`.
- `internal/usecase/channel` and `internal/access/api`:
  - `allow_stranger` is mapped into `metadb.Channel.AllowStranger`.
- `pkg/slot/meta`:
  - channel create / upsert / get and scan preserve `AllowStranger`.
- `pkg/slot/fsm`:
  - upsert-channel command encoding / decoding and inspection preserve `AllowStranger`.
- `pkg/slot/proxy`:
  - channel metadata binary RPC round-trips `AllowStranger` and old payloads default it to zero when feasible.

Suggested verification:

```bash
GOWORK=off go test ./internal/usecase/message -run 'AllowStranger|Person|SendBan' -count=1
GOWORK=off go test ./internal/usecase/channel ./internal/access/api ./pkg/slot/meta ./pkg/slot/fsm ./pkg/slot/proxy -count=1
GOWORK=off go test ./... -count=1
```

## Documentation Updates

After implementation, update:

- `docs/raw/send-path-business-logic-diff.md` to mark `AllowStranger` restored and describe the exact precedence;
- `pkg/slot/FLOW.md` to list `AllowStranger` in channel metadata and permission RPC behavior;
- `internal/FLOW.md` if the message/channel usecase flow descriptions mention the set of persisted channel flags or person permission steps.

No `wukongim.conf.example` update is expected because this design uses existing `WK_MESSAGE_PERSON_WHITELIST_ENABLED` behavior.
