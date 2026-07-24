# Embedded Operations MCP Flow

```text
POST /mcp
  -> reject any non-empty Origin
  -> enforce 64 KiB request bound
  -> generate a server-owned correlation ID; ignore client request IDs
  -> parse one wko_* bearer token
  -> ingress Verifier checks desired state and credential digest
  -> local owner: revalidate and execute
  -> non-owner Manager: apply ingress admission, bound preserved headers, then
     send typed internal RPC containing credential ID/digest, expected
     Controller revision, and bounded JSON payload
  -> owner revalidates enabled state, owner, revision, and digest
  -> stateless JSON Streamable HTTP MCP server
  -> exact frozen 12-tool registry
  -> opsobserve.Service and wukongim/ops-observation/v1
```

Manager JWTs are not MCP credentials. Authentication failures return HTTP 401;
disabled or unavailable execution returns a stable HTTP 503 error. The
protocol adapter does not expose Resources, Prompts, Sampling, Roots, SSE, or
any write tool. Unsupported methods, oversized bodies, malformed inputs, and
rate or concurrency rejections use stable public errors without exposing
internal implementation details. Tool arguments are decoded with unknown
fields rejected and invalid input is returned as MCP invalid-params.

Every configured Manager listener mounts the same `/mcp` endpoint. There is no
additional process, listener, port, or MCP TOML switch. Plain HTTP is accepted,
but Manager UI and operator documentation warn that TLS should be used across
untrusted networks. Browser origins are not trusted: non-empty `Origin` is
rejected and `/mcp` has no CORS grant.
