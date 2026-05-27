# wukongimv2 Entry Design

Date: 2026-05-27
Status: Draft for review
Owner: Codex

## 1. Purpose

`cmd/wukongimv2` will be the standalone executable entry for the new
`internalv2` architecture. It is not a runtime switch inside the existing
`cmd/wukongim` process.

The goal is to keep the old and new architecture boundaries clean:

```text
cmd/wukongim   -> internal/app   -> current production architecture
cmd/wukongimv2 -> internalv2/app -> new architecture
```

This avoids making the existing app root responsible for two dependency graphs,
two lifecycle models, and two cluster wiring strategies. `cmd/wukongim` remains
stable while `cmd/wukongimv2` evolves as the verification and migration entry
for the new stack.

Single-node deployment remains a single-node cluster. `cmd/wukongimv2` must not
add local send, storage, or routing shortcuts that bypass `pkg/clusterv2`.

## 2. Selected Approach

Use a separate binary entry:

- Add `cmd/wukongimv2`.
- Keep `cmd/wukongim` unchanged.
- Load the same `wukongim.conf` format where possible.
- Map only the fields required by `internalv2/app.Config` for the current phase.
- Start `internalv2/app.App` directly.
- Add process-level tests and e2e smoke coverage for the new binary.

Rejected alternatives:

- A `WK_INTERNALV2_GATEWAY_SEND_ENABLE` flag in `cmd/wukongim`: rejected because
  it would mix old and new app composition inside one process and make runtime
  behavior depend on a broad architectural switch.
- Replacing `cmd/wukongim` immediately: rejected because `internalv2` currently
  implements only the send-to-sendack skeleton, not delivery, conversation, CMD,
  management, or compatibility behavior.

## 3. Scope

The first `cmd/wukongimv2` milestone is a verification entry, not a full
production replacement.

In scope:

- `cmd/wukongimv2` thin main package.
- Config loading for the minimal single-node-cluster send path.
- Mapping config into `internalv2/app.Config`.
- Starting and stopping `internalv2/app.App`.
- Gateway listener startup through the existing `pkg/gateway` runtime.
- Smoke tests that send a WKProto `SendPacket` and observe a success
  `SendackPacket`.
- Documentation that makes the entry boundary explicit.

Out of scope for this milestone:

- Wiring `internalv2` into `cmd/wukongim`.
- Delivery, conversation, CMD, plugin, manager API, or admin UI migration.
- Multi-node production readiness claims.
- Compatibility with all legacy message fields.
- Docker image and deployment manifest changes unless needed by a later e2e
  plan.

## 4. Phase Order

### Phase 2a: Default clusterv2 Metadata Smoke

Before adding the executable, prove that the existing `internalv2/app` can send
through the default `pkg/clusterv2` single-node-cluster metadata path without
injecting static channel metadata in the test.

Target test:

```text
TestSingleNodeClusterFirstSendCreatesChannelMetaAndSendack
```

Expected behavior:

- Build a real `internalv2/app.App`.
- Use real default `pkg/clusterv2` single-node cluster wiring.
- Do not inject a static `channels.MetaSource`.
- Send one normal `SendPacket` through the gateway handler.
- Assert a success `SendackPacket`.
- Assert node-scoped message ID and `MessageSeq == 1`.
- Send a second packet to the same channel and assert `MessageSeq == 2`.

This phase validates the production-shaped cluster metadata path before adding
a new process entry around it.

### Phase 2b: Minimal `cmd/wukongimv2`

Add the standalone entry after Phase 2a passes.

Initial flow:

```text
main
  -> parse -config
  -> load wukongim.conf-style config
  -> map to internalv2/app.Config
  -> internalv2/app.New
  -> app.Start(ctx)
  -> wait for SIGINT/SIGTERM
  -> app.Stop(ctx)
```

The entry must stay thin. Business logic belongs in `internalv2/usecase/*`,
runtime adapters belong in `internalv2/infra/*`, and dependency wiring belongs
in `internalv2/app`.

### Phase 2c: Binary-Level Smoke

Add a black-box smoke that starts `cmd/wukongimv2`, connects through the gateway
listener, sends one WKProto message, and validates the returned sendack.

This verifies the real executable, config parser, gateway listener, protocol
codec, and `internalv2` send path together.

## 5. Config Strategy

`cmd/wukongimv2` should use the same config discovery order as `cmd/wukongim`:

```text
explicit -config
./wukongim.conf
./conf/wukongim.conf
/etc/wukongim/wukongim.conf
```

The file format remains `KEY=value`, and environment variables continue to use
the `WK_` prefix. Environment variables override file values.

The first implementation should parse only the fields required by the
single-node-cluster send path:

- Node identity.
- Data directory.
- Cluster listen address.
- Controller voter/listen data required by `pkg/clusterv2`.
- Gateway listener settings.
- Gateway send timeout.

If the existing key names map cleanly, reuse them. If a v2-only field is needed,
add a `WK_` key with detailed English comments and update
`wukongim.conf.example` in the same change.

Do not add a config flag that makes `cmd/wukongim` call `internalv2`.

## 6. Testing Strategy

Unit tests:

- Config file loading and environment override behavior.
- Config-to-`internalv2/app.Config` mapping.
- Main run helper starts and stops a fake app runtime on signal/cancel paths.

Package tests:

- `internalv2/app` default metadata smoke from Phase 2a.
- Existing `internalv2` gateway/usecase/adapter tests remain focused and fast.

Binary/e2e tests:

- Start `cmd/wukongimv2` with a temporary config and data dir.
- Connect with the real gateway protocol.
- Send one message and assert a success sendack.
- Keep the first e2e single-node-cluster only.

Verification commands:

```bash
go test ./cmd/wukongimv2 ./internalv2/... ./pkg/clusterv2 ./pkg/channelv2
go test ./test/e2e/message/... -run WukongIMV2
```

The exact e2e package and test name can be refined in the implementation plan.

## 7. Error Handling And Lifecycle

Startup must fail closed:

- Invalid config returns an error before starting runtimes.
- `internalv2/app.New` errors abort startup.
- Cluster startup errors abort gateway startup.
- Gateway startup errors trigger app rollback through `internalv2/app`.

Shutdown should use context-bounded stop semantics and log stop errors without
masking the original startup error.

`cmd/wukongimv2` should not implement its own lifecycle framework. It should
delegate runtime lifecycle to `internalv2/app`.

## 8. Documentation

When `cmd/wukongimv2` is implemented:

- Add it to `AGENTS.md` directory structure.
- Add or update `cmd/wukongimv2/README.md`.
- Add a concise project knowledge note if a new operational rule is discovered.
- Update `wukongim.conf.example` only if new config keys are introduced.

## 9. Acceptance Criteria

- `cmd/wukongim` remains unchanged and continues to use `internal/app`.
- `cmd/wukongimv2` uses `internalv2/app` directly.
- No runtime config switch routes old `cmd/wukongim` into `internalv2`.
- Single-node-cluster first-send smoke passes without static channel metadata.
- `cmd/wukongimv2` can start, accept a gateway send, and return a success
  sendack in a black-box test.
- Tests remain fast enough for normal unit-test workflows; longer real-process
  scenarios should be clearly scoped.
