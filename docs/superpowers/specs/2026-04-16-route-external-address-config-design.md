# Route External Address Config Design

**Context**

The compatibility endpoints in `internal/access/api/route.go` currently derive external route addresses from `WK_GATEWAY_LISTENERS`. That works for listener defaults, but it cannot represent the common deployment case where the actual listener binds `0.0.0.0` or an internal address while `/route` must publish a separately configured external TCP/WS/WSS address.

**Goal**

Support three new config keys:

- `WK_EXTERNAL_TCPADDR`
- `WK_EXTERNAL_WSADDR`
- `WK_EXTERNAL_WSSADDR`

These keys must affect only the payload returned by `/route` and `/route/batch`. They must not change the API listen address, gateway listener binding address, or any other runtime networking behavior.

**Design**

1. Extend `internal/app.APIConfig` with:
   - `ExternalTCPAddr string`
   - `ExternalWSAddr string`
   - `ExternalWSSAddr string`
2. Parse the new keys in `cmd/wukongim/config.go` and persist them in `app.Config`.
3. Update `internal/app/build.go` so the legacy route compatibility address injection prefers explicit API external address config and falls back to listener-derived addresses only when a field is empty.
4. Keep intranet route behavior unchanged.
5. Update `wukongim.conf.example` so the new config surface stays aligned with runtime behavior.

**Testing**

- Add config parsing coverage in `cmd/wukongim/config_test.go`.
- Add unit coverage in `internal/app` for the merge/fallback behavior that builds the legacy route address payload.
- Keep the existing API contract tests for `/route` and `/route/batch` intact.
