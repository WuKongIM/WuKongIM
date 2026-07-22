# E2E Suite Flow

`test/e2e/suite` owns reusable black-box process, configuration, protocol, and
HTTP helpers for real `cmd/wukongim` tests.

## Process lifecycle

1. `Suite` allocates a test workspace, loopback ports, and a short independent
   plugin socket root.
2. Config renderers write node TOML and derive the product environment.
3. The default binary cache builds `cmd/wukongim` with the `e2e` build tag;
   tagged product substitutes remain dormant unless their separate explicit
   harness environment is present. `NodeProcess.Start` removes the
   harness-only `WK_E2E_*` namespace before starting the child process.
4. Test cleanup stops the current process for every registered node, including
   nodes appended after cluster startup and processes replaced by restart.

`ReconfigureStoppedNodes` rewrites a static cluster generation only after all
nodes are stopped. It preserves data directories and non-product external
environment, replaces schema-known product config environment, and lets a
restore scenario restart the same successor data in normal mode without a
mixed restore/normal generation.

## Failure diagnostics

`NodeProcess.DumpDiagnostics` keeps output bounded and safe for CI logs:

- process state and artifact paths are always reported;
- config content is parsed as TOML and validated against the public
  `internal/config.SchemaFields` leaf and group-prefix contract before it is
  re-encoded and limited to the diagnostic tail;
- unknown paths, scalar values where schema groups are required, schema-leaf
  kind mismatches, and invalid TOML fail closed to the single
  `[invalid or unsupported TOML; content omitted]` marker; neither source
  content nor validation/parser errors are included in diagnostics;
- stdout, stderr, app, and error logs use the existing bounded tail path.

Every schema leaf marked `DiagnosticSensitive` is redacted as a whole value.
Startup-snapshot `Sensitive` fields inherit that diagnostic behavior, while the
additional URL-only diagnostic policy does not change startup snapshot output.
The complete URL values for `api.external_ws_addr`, `api.external_wss_addr`,
`webhook.http_addr`, and `prometheus.query_base_url` are diagnostic-sensitive so
userinfo, paths, query parameters, and fragments cannot leak.

Known ordinary structured leaves remain useful as evidence, but their nested
password, secret, token, credential, and private/API/access key values are
redacted using case-insensitive, separator-independent key matching.

## Public request helpers

HTTP helpers preserve typed non-2xx response details. Message-send recovery
retries only the exact public `503 {"error":"retry required"}` signal while
reusing one serialized request body and idempotency key.
