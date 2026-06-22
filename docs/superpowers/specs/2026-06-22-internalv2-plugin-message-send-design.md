# Internalv2 Plugin Message Send Design

## Goal

Migrate the PDK-compatible plugin-origin host RPC `/message/send` into `internalv2` so plugins can submit messages through the v2 message usecase without bypassing permissions, plugin hook recursion controls, NoPersist handling, or channel append routing.

## Scope

- Add `/message/send` to `internalv2/access/plugin`.
- Add plugin SendReq mapping and `SendMessage` orchestration to `internalv2/usecase/plugin`.
- Wire the v2 app plugin subsystem to the v2 message usecase through a narrow port.
- Cover the path with unit tests, app wiring tests, and plugin benchmarks.
- Update plugin flow documentation.

Out of scope for this step:

- Other legacy plugin host RPCs such as `/channel/messages`, `/cluster/config`, `/plugin/httpForward`, and conversation routes.
- Receive, Route, ConfigUpdate, and stream host RPC migration.
- New plugin configuration keys.

## Compatibility Rules

- The access layer decodes and encodes existing `internal/usecase/plugin/pluginproto` protobuf messages.
- `SendReq.fromUid` is optional. When empty, v2 uses `internalv2/usecase/user.DefaultSystemUID`, matching legacy behavior that used the built-in system UID rather than a separate config value.
- The mapped `message.SendCommand` must preserve:
  - `ClientMsgNo`
  - `FromUID`
  - `ChannelID`
  - `ChannelType`
  - cloned `Payload`
  - `NoPersist`
  - `SyncOnce`
  - `RedDot`
- For person channels, the command must set `NormalizePersonChannel=true` so a plugin can pass the receiver UID and still write to the canonical person channel.
- The command must set `Origin=message.SendOriginPlugin`.
- The command must not set `SkipPluginHooks`, so the existing v2 recursion guard remains authoritative.
- The response remains compatible with the PDK `SendResp` shape and returns `messageId` only.

## Architecture

```text
plugin process
  -> wkrpc /message/send
  -> internalv2/access/plugin.Server.handleSendMessage
  -> internalv2/usecase/plugin.App.SendMessage
  -> MessageSender.Send
  -> internalv2/usecase/message.App.Send
  -> Send hook recursion guard / permissions / channelappend
```

`internalv2/access/plugin` stays a protocol adapter. It owns route registration, protobuf body limits, request timeout, and response writing.

`internalv2/usecase/plugin` owns PDK compatibility mapping and depends on a narrow `MessageSender` port. This keeps business orchestration out of access and avoids making the plugin usecase depend on app internals.

`internalv2/app` wires `pluginMessageSender{app}` into plugin options. The adapter reads `a.messages` at call time, which safely handles the existing construction order: the plugin usecase must exist before `wireMessages()` so it can be injected as the `SendHook`, while plugin-origin message sending only happens after startup.

## Failure Semantics

- Missing message sender returns `ErrMessageSenderRequired`.
- Missing plugin default sender when `fromUid` is empty returns `ErrDefaultSenderUIDRequired`; app wiring supplies the v2 default system UID, so this only affects direct tests or miswired apps.
- Message usecase errors propagate to host RPC `WriteErr`.
- Non-success business reasons from the message usecase are preserved only through the message path. `SendResp` cannot carry reason because the legacy proto only exposes `messageId`.

## Performance Notes

The hot path remains the normal message usecase and channelappend path. The plugin host RPC adds protobuf decode, command mapping, payload clone, and response encode. Benchmarks should cover command mapping and handler round-trip overhead so future plugin host RPC work has a baseline.

## Test Plan

- `internalv2/usecase/plugin`: verify mapping, default sender fallback, payload clone, NoPersist/SyncOnce/RedDot, person-channel normalization, origin marker, and missing dependency errors.
- `internalv2/access/plugin`: verify route registration includes `/message/send`, handler decodes request, applies timeout, passes caller UID, writes `SendResp`, and propagates errors.
- `internalv2/app`: verify plugin subsystem binds host RPC sending to `app.Messages()` without breaking the existing Send hook wiring.
- Benchmarks:
  - `BenchmarkSendMessageFromPluginReq`
  - `BenchmarkMessageSendHostRPCHandler`

## Self Review

- Spec covers access, usecase, app wiring, docs, and benchmark surfaces.
- It does not add config because legacy default sender is the built-in system UID.
- It keeps `access -> usecase -> port` boundaries intact.
- It explicitly preserves person-channel normalization, which is the main subtle compatibility risk.
