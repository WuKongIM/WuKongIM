# internalv2 Plugin HTTP Forward Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Support the legacy `/plugin/httpForward` host RPC in `internalv2`.

**Architecture:** `internalv2/access/plugin` decodes the host RPC and delegates
to `internalv2/usecase/plugin`. The usecase normalizes request data, calls local
plugin `/plugin/route` through the invoker, or forwards one normalized request to
a selected node through a narrow `HTTPForwarder` port backed by
`internalv2/access/node` manager plugin RPC.

**Tech Stack:** Go, protobuf `pluginproto`, clusterv2 node RPC, existing
internalv2 plugin usecase/access/infra/app layering, `go test`, benchmark tests.

---

### Task 1: Usecase Local And Remote HTTP Forward

**Files:**
- Create: `internalv2/usecase/plugin/host_rpc_http.go`
- Create: `internalv2/usecase/plugin/host_rpc_http_test.go`
- Modify: `internalv2/usecase/plugin/types.go`
- Modify: `internalv2/usecase/plugin/app.go`
- Modify: `internalv2/usecase/plugin/invocation.go`
- Modify: `internalv2/usecase/plugin/benchmark_test.go`

- [ ] Write failing tests for local route invocation, caller plugin fallback,
  hop-by-hop header removal, remote node forwarding, fanout deferred, body
  limit, header/query limit, and forwarder error propagation.
- [ ] Run `go test ./internalv2/usecase/plugin -run 'TestHTTPForward|TestRoute' -count=1`
  and confirm missing symbols or failing behavior.
- [ ] Add `PathRoute`, `HTTPForwarder`, `HTTPForwardMaxBodyBytes`, HTTP forward
  errors, `App.Route`, and `App.HTTPForward`.
- [ ] Add helper functions for cloning HTTP requests/responses, stripping
  hop-by-hop headers, and measuring header/query bytes.
- [ ] Run the same focused test command and confirm it passes.
- [ ] Add `BenchmarkHTTPForwardLocalRoute` in
  `internalv2/usecase/plugin/benchmark_test.go`.

### Task 2: Host RPC Access Route

**Files:**
- Modify: `internalv2/access/plugin/server.go`
- Modify: `internalv2/access/plugin/handlers_message.go`
- Modify: `internalv2/access/plugin/server_test.go`

- [ ] Write failing tests for `/plugin/httpForward` route registration, request
  decode, caller plugin fallback, timeout context, shorter incoming deadline,
  response encoding, and access-layer max body rejection.
- [ ] Run `go test ./internalv2/access/plugin -run 'TestServerRegisters|TestHandleHTTPForward|TestHTTPForwardHostRPC' -count=1`
  and confirm route/interface failures.
- [ ] Extend the access `Usecase` interface with `HTTPForward`.
- [ ] Register `/plugin/httpForward` and implement `handleHTTPForward`.
- [ ] Run the same focused test command and confirm it passes.
- [ ] Add `BenchmarkHTTPForwardHostRPCHandler`.

### Task 3: Node RPC Forward Operation

**Files:**
- Modify: `internalv2/access/node/manager_plugins_codec.go`
- Modify: `internalv2/access/node/manager_plugins_rpc.go`
- Modify: `internalv2/access/node/manager_plugins_rpc_test.go`
- Modify: `internalv2/access/node/manager_plugins_benchmark_test.go`
- Modify: `internalv2/access/node/FLOW.md`

- [ ] Write failing tests for manager plugin RPC `http_forward` handling and
  `Client.ForwardPluginHTTP`.
- [ ] Run `go test ./internalv2/access/node -run 'TestManagerPluginRPC.*HTTP|TestPluginHTTPForward' -count=1`
  and confirm codec/client failures.
- [ ] Extend the manager plugin RPC request/response structs with
  `ForwardReq` and `ForwardResp`.
- [ ] Add deterministic binary encoding for `pluginproto.ForwardHttpReq`,
  `pluginproto.HttpRequest`, and `pluginproto.HttpResponse`.
- [ ] Extend `ManagerPluginReader` with `ForwardPluginHTTP`.
- [ ] Handle `managerPluginOpHTTPForward` by calling only local route execution
  on the target node.
- [ ] Run the same focused test command and confirm it passes.
- [ ] Add node RPC benchmark coverage for HTTP forward encode/decode.

### Task 4: Infra And App Wiring

**Files:**
- Create: `internalv2/infra/cluster/plugin_http_forward.go`
- Create: `internalv2/infra/cluster/plugin_http_forward_test.go`
- Modify: `internalv2/app/plugin.go`
- Modify: `internalv2/app/plugin_test.go`
- Modify: `internalv2/app/FLOW.md`
- Modify: `internalv2/infra/cluster/FLOW.md`

- [ ] Write failing infra and app tests proving positive `toNodeId` plugin HTTP
  forward routes through clusterv2 node RPC.
- [ ] Run `go test ./internalv2/infra/cluster ./internalv2/app -run 'TestPluginHTTPForward|TestNewWiresPluginUsecaseAsHTTPForwarder' -count=1`
  and confirm missing constructor/wiring failures.
- [ ] Add `PluginHTTPForwardNode` and `PluginHTTPForwarder`.
- [ ] Wire `pluginOptions.HTTPForwarder` from `internalv2/app` when the cluster
  can call node RPC.
- [ ] Ensure the manager plugin RPC adapter receives the plugin usecase directly
  so remote `http_forward` can execute local route hooks.
- [ ] Run the same focused test command and confirm it passes.
- [ ] Update FLOW files.

### Task 5: Verification And Commit

**Files:**
- All files changed by Tasks 1-4

- [ ] Run focused tests:
  `go test ./internalv2/usecase/plugin ./internalv2/access/plugin ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/app -count=1`
- [ ] Run broader v2 tests: `go test ./internalv2/...`
- [ ] Run benchmarks:
  `go test ./internalv2/usecase/plugin ./internalv2/access/plugin ./internalv2/access/node -run '^$' -bench 'Benchmark(HTTPForward|ManagerPluginRPC.*HTTP|PluginHTTPForward)' -benchmem`
- [ ] Run `git diff --check` and `git diff --cached --check`.
- [ ] Commit with `git commit -m "feat: support internalv2 plugin http forward"`.
