# Remove Progress Ack Design

## Summary

This design removes the legacy `progress_ack` steady-state replication path from `pkg/channel` and collapses the runtime onto a single steady-state replication protocol: `long_poll`.

After this change, `WK_CLUSTER_REPLICATION_MODE` is no longer a meaningful configuration surface because there is only one supported replication behavior. The runtime always uses `long_poll`, while the legacy config key is silently ignored for backward compatibility with existing configuration files.

## Goals

- Remove the legacy `progress_ack` steady-state replication implementation.
- Make `long_poll` the only steady-state replication path.
- Remove `WK_CLUSTER_REPLICATION_MODE` from the active configuration model.
- Silently ignore legacy `WK_CLUSTER_REPLICATION_MODE` in config files and environment input.
- Keep `ReconcileProbe` as the recovery path.
- Keep the existing long-poll tuning knobs.

## Non-Goals

- No mixed-version compatibility layer for old replication protocols.
- No attempt to preserve `progress_ack` transport RPCs as aliases.
- No change to cluster semantics beyond removing the legacy replication mode.

## Design

### Configuration

- Remove parsing, validation, defaults, and stored config fields for `WK_CLUSTER_REPLICATION_MODE`.
- Keep parsing for long-poll tuning knobs:
  - `WK_CLUSTER_LONG_POLL_LANE_COUNT`
  - `WK_CLUSTER_LONG_POLL_MAX_WAIT`
  - `WK_CLUSTER_LONG_POLL_MAX_BYTES`
  - `WK_CLUSTER_LONG_POLL_MAX_CHANNELS`
- If `WK_CLUSTER_REPLICATION_MODE` is present in config or environment, ignore it without error.
- Update `wukongim.conf.example` and config-facing docs to stop advertising replication mode selection.

### Runtime and Transport

- Remove `ReplicationMode` fields and branching from `internal/app`, `pkg/channel`, `pkg/channel/runtime`, and `pkg/channel/transport`.
- Treat steady-state replication as always long-poll.
- Remove legacy `ProgressAck` envelope, codec, RPC handler, session send path, and runtime handling.
- Remove legacy fetch-batch transport path that existed only for `progress_ack` steady-state replication.
- Preserve `Fetch` service support only where needed by long-poll internals or tests; do not preserve legacy steady-state fetch batching behavior.
- Preserve `ReconcileProbe` request/response transport and replica handling.

### Tests

Follow TDD for the behavior change:

1. Add config tests proving:
   - default app config no longer stores a replication mode,
   - `WK_CLUSTER_REPLICATION_MODE` is ignored,
   - long-poll knobs still work.
2. Add transport/runtime tests proving:
   - progress-ack RPC/service identifiers are gone,
   - progress-ack envelopes are no longer dispatched,
   - long-poll request flow still works.
3. Update any app/runtime tests that were explicitly setting `ReplicationMode = "long_poll"` so they use the new single-mode defaults.

### Documentation

Update flow and config docs to reflect the new single-mode model:

- `pkg/channel/FLOW.md`
- any `internal/app` / config docs that mention replication mode selection
- `wukongim.conf.example`

## Risks and Mitigations

- **Risk:** lingering tests or helpers still assume `progress_ack` exists.
  - **Mitigation:** remove dead helpers and update focused suites in the same change.
- **Risk:** older local config files still set `WK_CLUSTER_REPLICATION_MODE`.
  - **Mitigation:** silently ignore the key instead of failing startup.
- **Risk:** docs drift after removing code paths.
  - **Mitigation:** update `FLOW.md` in the same patch.

## Acceptance Criteria

- No production code path references `progress_ack` as a replication mode.
- No active config field or validation requires `WK_CLUSTER_REPLICATION_MODE`.
- Legacy `WK_CLUSTER_REPLICATION_MODE` input does not break startup.
- Steady-state channel replication runs only through `long_poll`.
- Focused config, runtime, and transport tests pass.
