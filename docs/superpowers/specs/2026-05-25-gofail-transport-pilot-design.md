# Gofail Transport Pilot Design

## Summary

Add an opt-in gofail pilot for WuKongIM's node transport path. The pilot must not change normal builds, production config, or default tests. It provides a repeatable way to build a failpoint-enabled `cmd/wukongim` binary and drive focused e2e scenarios through the existing `WK_E2E_BINARY` hook.

## Goals

- Keep failpoints absent from normal source output until explicitly enabled by `gofail enable`.
- Add a small set of transport failpoints that can delay or fail node-to-node send/RPC calls.
- Let e2e tests pass per-node environment variables so three-node tests can use separate `GOFAIL_HTTP` ports.
- Provide a script that creates a temporary source copy, enables failpoints, builds the binary, and leaves the main worktree untouched.

## Non-Goals

- No production `WK_*` config for failpoints.
- No broad chaos framework or random failure matrix.
- No replacement for existing unit-test hooks such as `LocalNetwork` drop toggles or raftlog testing hooks.
- No default CI requirement to run failpoint e2e tests.

## Architecture

`pkg/transport` gets two narrow failpoints at the Pool boundary:

- `wkTransportSendFault`: can return an injected error before an outbound non-RPC send is enqueued, or sleep and continue when configured with a `sleep` term.
- `wkTransportRPCFault`: can return an injected error before an outbound RPC is enqueued, or sleep and continue when configured with a `sleep` term.

The generated failpoint runtime dependency is introduced only through files produced by `gofail enable` in a temporary source copy. Normal source remains valid without importing `go.etcd.io/gofail/runtime`.

`test/e2e/suite` adds per-node environment support to `NodeProcess`. Config rendering stays unchanged; callers can pass `GOFAIL_FAILPOINTS` or `GOFAIL_HTTP` only for selected nodes. The initial opt-in e2e smoke uses `GOFAIL_HTTP` to prove registration before later scenarios inject actual transport failures.

`scripts/build-gofail-binary.sh` copies the repository to a temporary directory, runs `gofail enable` for selected packages, builds `./cmd/wukongim`, and prints the binary path for use with `WK_E2E_BINARY`.

## Testing

- Unit tests cover e2e env propagation without launching real `wukongim`.
- Transport package tests verify that source failpoint marker comments stay present and can be parsed by a lightweight script-level check.
- Script tests validate command composition and failpoint package defaults without running long e2e scenarios.
- `test/e2e/cluster/gofail_transport` is an opt-in smoke that starts a gofail-enabled binary and verifies the transport failpoints are registered through `GOFAIL_HTTP`.
- Full fault-injection e2e matrices are not added to default test runs.
