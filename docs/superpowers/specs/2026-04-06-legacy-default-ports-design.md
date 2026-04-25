# Legacy Default Ports Compatibility Design

**Goal**

Make the current `WuKongIM` main program use the same default API and long-connection ports as the legacy version when the JSON config omits those fields.

**Legacy Baseline**

From `learn_project/WuKongIM/internal/options/options.go`, the legacy defaults are:

- API HTTP listen address: `0.0.0.0:5001`
- TCP long connection listen address: `tcp://0.0.0.0:5100`
- WebSocket listen address: `ws://0.0.0.0:5200`

For the current project, only the effective bind ports need to match. The current transport and protocol model remains unchanged.

**Scope**

- Add default API listen address compatibility for `cmd/wukongim`
- Add default gateway listener compatibility for `cmd/wukongim`
- Preserve explicit JSON configuration values

Out of scope:

- Legacy config field aliases such as `httpAddr`, `addr`, `wsAddr`, `external.*`
- Manager port compatibility
- Reintroducing legacy derived external addresses
- Changing `internal/app` config semantics

**Design**

Compatibility is applied only in the JSON loading layer at `cmd/wukongim/config.go`.

Rules:

1. If `api.listenAddr` is omitted from the JSON config, default it to `0.0.0.0:5001`.
2. If `gateway.listeners` is omitted from the JSON config, default it to:
   - `binding.TCPWKProto("tcp-wkproto", "0.0.0.0:5100")`
   - `binding.WSJSONRPC("ws-jsonrpc", "0.0.0.0:5200")`
3. If those fields are explicitly provided, preserve the provided values exactly.

This keeps the compatibility behavior limited to the `cmd` entrypoint and avoids changing the meaning of `app.Config{API: APIConfig{}}`, which currently allows API startup to remain disabled in direct programmatic construction.

**Files**

- Modify `cmd/wukongim/config.go`
- Modify `cmd/wukongim/config_test.go`

**Testing**

Add config-loading tests for:

- omitted `api.listenAddr` defaults to `0.0.0.0:5001`
- omitted `gateway.listeners` defaults to TCP `5100` and WebSocket `5200`
- explicit `api.listenAddr` remains unchanged
- explicit `gateway.listeners` remain unchanged

Keep existing `internal/app/config_test.go` behavior unchanged to verify the compatibility layer stays in `cmd`.
