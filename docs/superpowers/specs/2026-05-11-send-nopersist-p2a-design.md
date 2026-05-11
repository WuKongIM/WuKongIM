# Send NoPersist P2a Design

## Overview

P2a restores the core legacy `NoPersist` send semantics: a message marked `NoPersist=true` must pass normal send validation and permissions, then return a successful send result without writing the message to the durable channel log.

This phase intentionally restores only the non-durable boundary. It does not restore `SyncOnce`, cmd-channel conversion, request-scoped subscribers, temporary-channel delivery, `sendbatch`, plugin hooks, or webhook behavior.

## Goals

- Keep all existing P0/P1 send-before-append permission checks active for `NoPersist` messages.
- Skip durable channel-log append when `cmd.Framer.NoPersist` is true.
- Do not require `ChannelCluster`, meta refresher, or remote appender for `NoPersist` sends.
- Do not submit committed-message events for `NoPersist` sends, because no durable commit exists.
- Return `SendResult{Reason: frame.ReasonSuccess}` with zero `MessageID` and zero `MessageSeq` for successful non-durable sends.
- Add minimal HTTP `/message/send` compatibility for `header.no_persist`, with a top-level `no_persist` alias if cheap.

## Non-Goals

- No `SyncOnce` cmd-channel conversion.
- No request-scoped `subscribers` support.
- No temporary-channel or message-scoped subscriber-source delivery target wiring.
- No `/message/sendbatch`.
- No plugin, `PersistAfter`, offline webhook, or storage webhook integration.
- No claim that real-time online delivery for `NoPersist` is fully restored in P2a.

## Legacy Behavior Reference

In legacy `learn_project/WuKongIM`, `NoPersist` is carried in the send packet header. The channel persist stage skips storage when `NoPersist` is set, and downstream retry/webhook behavior treats non-persistent messages differently.

Relevant legacy references:

- `learn_project/WuKongIM/internal/api/message_model.go` accepts `header.no_persist`.
- `learn_project/WuKongIM/internal/api/message.go` maps HTTP header flags into `wkproto.SendPacket`.
- `learn_project/WuKongIM/internal/channel/handler/event_persist.go` only persists when `!sendPacket.NoPersist`.
- `learn_project/WuKongIM/internal/pusher/handler/event_pushonline.go` only builds retry state for persistent messages.

P2a adopts only the no-durable-write part in the current architecture.

## Current Architecture Fit

`internal/usecase/message.App.Send` already centralizes send validation, permission checks, and durable append dispatch. P2a should keep this layering:

1. Access adapters convert protocol/API input to `message.SendCommand`.
2. `message.App.Send` validates sender and channel type.
3. Person channel IDs are normalized.
4. `checkSendPermission` enforces P0/P1 business permissions.
5. If `cmd.Framer.NoPersist` is true, return success before cluster-required checks and before `sendDurable`.
6. Otherwise keep the existing durable path unchanged.

This keeps `pkg/channel/handler/append.go` business-rule free and avoids introducing any deployment branch that bypasses cluster semantics.

## HTTP Request Compatibility

Current `/message/send` accepts only simple fields. P2a adds a minimal header shape:

```json
{
  "from_uid": "u1",
  "channel_id": "u2",
  "channel_type": 1,
  "client_msg_no": "m1",
  "payload": "aGk=",
  "header": { "no_persist": 1 }
}
```

Implementation should map non-zero `header.no_persist` to `frame.Framer{NoPersist: true}`. A top-level `no_persist` alias may also be accepted to ease internal tests and simple clients, but `header.no_persist` is the compatibility contract.

No other header fields are in scope for P2a.

## Send Result Semantics

For successful non-durable sends:

- `Reason` is `frame.ReasonSuccess`.
- `MessageID` is `0`.
- `MessageSeq` is `0`.
- No committed event is dispatched.
- No durable send trace event is emitted, because the durable stage does not run.

Permission denials keep their existing reason behavior. Infrastructure errors from permission reads still return errors before the non-durable shortcut.

## Testing Strategy

- `internal/usecase/message`:
  - `NoPersist` without a configured cluster returns `ReasonSuccess`.
  - `NoPersist` does not call channel append and does not submit committed events.
  - `NoPersist` still respects P0/P1 permissions, e.g. sender `SendBan` or group `Disband` rejects before the shortcut.
  - Durable sends remain unchanged.
- `internal/access/api`:
  - `/message/send` parses `header.no_persist` and passes `Framer.NoPersist=true` to the message usecase.
  - Existing request shapes remain compatible.

## Rollout Notes

- No config changes are required.
- No slot metadata, FSM, or proxy schema changes are required.
- Update `docs/raw/send-path-business-logic-diff.md` after implementation to mark P2a as restoring only the non-durable send boundary, not full `SyncOnce` or temporary delivery semantics.
