# internalv2 Plugin HTTP Forward Fanout Decision Design

## Scope

Define the compatibility behavior for `/plugin/httpForward` requests whose
`toNodeId` is `-1`.

This spec is intentionally narrow. It does not introduce a new broadcast host
RPC, does not change the PDK protobuf schema, and does not add public HTTP API
behavior.

## Context

`pluginproto.ForwardHttpReq.toNodeId` documents three modes:

- `0`: current node
- positive node ID: selected target node
- `-1`: all nodes

The migrated `internalv2` implementation already supports local and positive
node routing. Both the legacy `internal` implementation and the current
`internalv2` implementation return `ErrHTTPForwardFanoutDeferred` for `-1`, so
there is no existing production fanout behavior to preserve.

## Decision

Keep `toNodeId=-1` explicitly deferred.

When a plugin calls `/plugin/httpForward` with `toNodeId=-1`, the host returns
the typed fanout-deferred error. This preserves the legacy-compatible RPC
surface while avoiding an ambiguous and potentially expensive synchronous
cluster broadcast.

## Rationale

`pluginproto.HttpResponse` represents one HTTP-style response. A true all-node
fanout needs per-node status, error, and response data. Returning only the first
success would hide partial failure; returning a synthesized body would create a
new undocumented response contract; failing the whole call on one node would make
large-cluster behavior fragile.

Synchronous all-node fanout also creates O(N) RPC work from one plugin host RPC.
That is not acceptable as an accidental compatibility path in deployments with
many nodes, many active plugins, and high plugin-origin request volume.

## Alternatives Considered

### Selected: explicit deferred error

Keep the current typed error and document it as the supported compatibility
contract for now.

This is the safest behavior because it is deterministic, cheap, and matches the
current legacy implementation.

### Rejected: best-effort fanout with first successful response

This preserves a single `HttpResponse`, but it loses node-level failure
information and makes the result depend on scheduling and timeout timing.

### Rejected: encoded aggregate response body

This could return every node result, but it would silently introduce a new
contract inside a legacy single-response message. Plugins would need custom
parsing logic not described by the PDK protobuf schema.

### Future Option: new aggregate host RPC

If plugins need cluster-wide route fanout later, add a v2-only aggregate host
RPC with an explicit response schema:

- per-node status and error
- bounded parallelism
- clear timeout and partial-success policy
- metrics for fanout size, latency, and failure count

That should be designed separately instead of overloading `/plugin/httpForward`.

## Data Flow

```text
plugin /plugin/httpForward host RPC
  -> access/plugin decodes pluginproto.ForwardHttpReq
  -> App.HTTPForward normalizes pluginNo, headers, query, and body
  -> toNodeId == -1
  -> return ErrHTTPForwardFanoutDeferred
```

No clusterv2 snapshot scan, node loop, retry loop, or remote RPC is performed for
this path.

## Error Handling

The error is intentional and typed:

- callers can detect it as `ErrHTTPForwardFanoutDeferred`
- access/plugin returns the host RPC error to the plugin process
- the request must not be routed locally as if `toNodeId=0`
- the request must not partially fan out to some nodes

## Testing

Add a real `.wkp` e2ev2 negative smoke under `test/e2ev2/plugin/http_forward`:

- start the existing three-node internalv2 cluster scenario
- have the initiator plugin call `/plugin/httpForward` with `toNodeId=-1`
- record the host RPC error in the plugin sandbox JSONL file
- assert the error contains the deferred fanout signal
- keep the existing local and positive-node success assertions

Unit coverage already exists for the usecase-level typed error. The new e2ev2
test closes the compatibility gap at the real plugin process boundary.

## Non-Goals

- No protobuf regeneration
- No new configuration
- No fanout metrics yet, because no fanout work is performed
- No retry, quorum, or partial-success behavior
