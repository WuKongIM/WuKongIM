# Debug API Config Unification Design

## Goal

Unify all HTTP `/debug...` route exposure behind one configuration key:
`WK_DEBUG_API_ENABLE`.

This is a breaking cleanup. The old split keys are removed instead of kept as
compatibility aliases.

## Scope

The unified switch controls every route whose path starts with `/debug`:

- `/debug/config`
- `/debug/cluster`
- `/debug/goroutines`
- `/debug/pprof`
- `/debug/pprof/*`
- `/debug/diagnostics/*`
- future debug routes such as `/debug/runtime/pools`

The following keys are removed from config loading, examples, docs, scripts,
and tests:

- `WK_HEALTH_DEBUG_ENABLE`
- `WK_PPROF_ENABLE`
- `WK_DIAGNOSTICS_DEBUG_API_ENABLE`

The following keys remain because they do not directly expose `/debug` routes:

- `WK_METRICS_ENABLE` controls `/metrics`.
- `WK_HEALTH_DETAIL_ENABLE` controls health/readiness response detail.
- `WK_DIAGNOSTICS_ENABLE` controls whether diagnostics data is collected.
- `WK_DIAGNOSTICS_DEBUG_MATCHES` remains a diagnostics sampling configuration
  for event capture; it does not expose routes.

## Runtime Semantics

`WK_DEBUG_API_ENABLE=false` means no `/debug...` routes are registered.

`WK_DEBUG_API_ENABLE=true` registers all available debug routes for the process.
Diagnostics debug routes still require a configured diagnostics reader/store.
If diagnostics collection is disabled, diagnostics debug routes return a clear
not-configured response rather than silently exposing empty data.

The switch is node-local. It does not create a cluster-wide manager surface and
does not bypass existing cluster semantics.

## Code Shape

Rename internal observability fields to match the new meaning:

- `HealthDebugEnabled` becomes `DebugAPIEnabled`.
- `PProfEnabled` is removed as a separate app/API option.
- `Diagnostics.DebugAPIEnabled` is removed.

`internal/access/api` and `internalv2/access/api` register all `/debug` routes
from a single `DebugAPIEnabled` option. Route handlers remain thin adapters;
debug data assembly stays in app/runtime packages.

Config parsers for `cmd/wukongim` and `cmd/wukongimv2` read only
`WK_DEBUG_API_ENABLE` for debug route exposure. Passing removed keys must fail
the existing strict/unknown-key validation path.

## Tests

Update config tests to cover:

- `WK_DEBUG_API_ENABLE=true` enables the app/API debug option.
- `WK_DEBUG_API_ENABLE=false` leaves debug routes disabled.
- removed keys are no longer accepted by strict config tests.

Update API route tests to cover:

- all `/debug...` routes are absent when debug API is disabled.
- config, cluster, goroutines, pprof, and diagnostics debug routes are governed
  by the same enabled option.

Run focused tests for config and API packages first. Broaden to the relevant
app packages if option wiring changes cross composition roots.
