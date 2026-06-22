# internalv2 Plugin HTTP Forward Design

## Scope

Migrate the legacy plugin host RPC `/plugin/httpForward` to `internalv2`.
This RPC lets a plugin ask the host to invoke another plugin's HTTP-style route
hook either on the current node or on one selected cluster node.

Out of scope:

- Broadcast fanout for `toNodeId=-1`
- Public HTTP API changes
- Plugin process lifecycle changes
- Route authorization policy beyond the existing plugin identity checks

## Compatibility Contract

- Decode and encode the existing `pluginproto.ForwardHttpReq` and
  `pluginproto.HttpResponse` messages.
- If `pluginNo` is empty, use the authenticated caller UID.
- Validate that the final plugin number is non-empty after trimming.
- `toNodeId=0`: invoke the local plugin route hook at `/plugin/route`.
- `toNodeId>0`: forward to that node through clusterv2 node RPC, and have the
  remote node invoke only its local plugin route hook.
- `toNodeId=-1`: return `ErrHTTPForwardFanoutDeferred`.
- Clone request and response bodies before crossing boundaries.
- Drop hop-by-hop request headers, including tokens listed by the `Connection`
  header.
- Enforce a default 10 MiB body limit on request and response bodies.
- Enforce a default 64 KiB aggregate header/query size limit.
- Propagate local invoker and remote RPC errors.

## Architecture

```text
plugin /plugin/httpForward host RPC
  -> internalv2/access/plugin.Server.handleHTTPForward
  -> internalv2/usecase/plugin.App.HTTPForward
       normalize pluginNo, headers, query, and body limits
       toNodeId=0: App.Route -> Invoker.RequestPlugin(pluginNo, /plugin/route)
       toNodeId>0: HTTPForwarder.ForwardPluginHTTP
       toNodeId=-1: ErrHTTPForwardFanoutDeferred

remote node path:
  -> internalv2/infra/cluster.PluginHTTPForwarder
  -> internalv2/access/node.Client.ForwardPluginHTTP
  -> clusterv2 RPCManagerPlugins op=http_forward
  -> internalv2/access/node.Adapter.HandleManagerPluginRPC
  -> plugin usecase LocalHTTPRoute
  -> Invoker.RequestPlugin(pluginNo, /plugin/route)
```

The node RPC extension reuses `RPCManagerPlugins` because it already represents
selected-node plugin capabilities. The new operation is a local-route operation,
not a second `/plugin/httpForward` dispatch, so remote calls cannot recursively
fan out to additional nodes.

## Error Boundaries

- Access-layer body limits protect the host RPC frame before protobuf decode.
- Usecase body/header limits protect plugin route payloads before local or remote
  execution.
- The remote node converts local plugin route errors into existing node RPC
  statuses where possible and returns rejected status for unknown route failures.
- The forwarding usecase receives typed context cancellation/deadline errors and
  rejected plugin-node errors from the node client.

## Performance Notes

- The hot path performs one protobuf decode at the plugin host RPC boundary and
  one route marshal for local execution.
- Remote forwarding performs one deterministic node RPC encode/decode pair and
  one route marshal on the target node.
- No broadcast or retry loop is introduced in this migration.
- Benchmarks cover local usecase routing, host RPC handler overhead, and node RPC
  forward encode/decode overhead.

## Verification

- Usecase unit tests for local route, caller plugin fallback, header stripping,
  remote node forwarding, fanout deferred, body/header limits, and error
  propagation.
- Access tests for `/plugin/httpForward` route registration, decode, caller
  fallback, deadline handling, and response encoding.
- Node RPC tests for encoded `http_forward` requests and client routing.
- Infra/app wiring tests for remote forwarder installation and manager plugin RPC
  registration.
- Benchmarks with `-benchmem` for usecase, access, and node RPC paths.
