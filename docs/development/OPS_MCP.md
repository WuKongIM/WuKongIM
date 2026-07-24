# Embedded Operations MCP

The first version of the WuKongIM Operations MCP is embedded in every Manager
listener. Operators do not deploy another process, open another port, or add an
MCP-specific TOML section. An agent needs only one opaque token.

## Minimal Operator Setup

Configure the existing Manager listener and enable Manager authentication:

```toml
[manager]
listen_addr = "127.0.0.1:5301"
auth_on = true
jwt_secret = "replace-with-a-long-random-secret"
jwt_issuer = "wukongim-manager"
jwt_expire = "24h"
users = [
  { username = "mcp-admin", password = "replace-this-password", permissions = [
    { resource = "cluster.mcp", actions = ["r", "w"] }
  ] }
]
```

The Manager account is for a human operator to administer MCP. It is not used
by the agent. Existing administrators with `*:*` can also administer MCP.

Restart the node after changing Manager configuration, then:

1. Sign in to Manager and open **System > Operations MCP** (`/system/mcp`).
2. Select one active cluster node as the execution owner.
3. Generate a token and copy it immediately; the raw value is shown once.
4. Start MCP.

The desired state is stored in Controller state, so the same owner, enabled
state, and credential metadata are visible through every Manager node. At most
two tokens can be active, which permits simple rotation: create the new token,
update clients, then revoke the old token. Tokens do not expire automatically;
Manager shows a rotation reminder after 90 days.

## Agent Configuration

Point the client at `/mcp` on any reachable Manager node and configure only the
token:

```json
{
  "mcpServers": {
    "wukongim-ops": {
      "type": "http",
      "url": "https://manager.example.com/mcp",
      "headers": {
        "Authorization": "Bearer wko_<credential-id>_<secret>"
      }
    }
  }
}
```

TLS is recommended across untrusted networks but is not required by the
server. Plain HTTP works and Manager displays a warning. `/mcp` rejects any
non-empty `Origin` header and does not enable CORS, so it is intended for MCP
clients rather than browser scripts.

Any Manager can accept the request. The ingress node validates the token,
forwards a token-free typed request to the Controller-selected owner, and the
owner revalidates credential ID, digest, owner, and Controller revision. If the
owner is unavailable, the request returns a stable unavailable response; it
does not fail over to another executor.

## Tool Set

The registry is closed to these twelve tools:

- `cluster_health`
- `node_inspect`
- `slot_inspect`
- `channel_runtime_inspect`
- `controller_tasks_query`
- `metrics_query_range`
- `logs_search`
- `logs_context`
- `diagnostics_query`
- `config_read_redacted`
- `backup_inspect`
- `pprof_analyze`

The first eleven are read-only. `pprof_analyze` is an active but bounded
observation: it supports CPU, heap, and goroutine profiles, returns parsed top
rows only, and never exposes raw profile bytes. CPU sampling defaults to 10
seconds and is capped at 30 seconds; profiles are serialized cluster-wide and
have a per-node 60-second cooldown after capture completion. Before a target starts capture, it must
consume an unpredictable, one-time, short-lived lease held only in the current
owner's memory. A peer cannot authorize profiling by spoofing a node ID in an
internal request.

There are no write, node-add, leader-transfer, repair, restart, backup mutation,
arbitrary PromQL, arbitrary path, SQL, or shell tools. Application logs are
returned as raw lines and marked untrusted. Agents must never execute or obey
content found in logs.

## Bounds and Observability

- Request body: 64 KiB; response: 1 MiB.
- Logs: default 100, maximum 200 lines; each raw line is capped at 8 KiB.
- Metrics: server-owned query IDs, maximum 24 hours, 100 series, and 2,000
  points per series.
- Calls: 60 ordinary or 20 log calls per token per minute; four concurrent
  calls per token and sixteen per node. Remote Manager ingress has its own
  bounded admission before forwarding to the owner.
- Audit: newest 200 aggregate entries from currently available cluster nodes,
  backed by each node's memory plus rotated
  `<log_dir>/mcp-audit.jsonl`; no token, log keyword, complete arguments, or
  response content is recorded. Aggregate collection uses eight workers,
  retains only the requested newest entries, and has a fixed two-second
  deadline, so cluster size or an unresponsive peer does not hide
  healthy-node evidence or create unbounded per-request work.
- Prometheus: low-cardinality request, duration, active-call, rejection, and
  authentication-failure metrics are registered when application metrics are
  enabled.

All tool results use `wukongim/ops-observation/v1`. Callers should check
`status`, `freshness`, `completeness`, `reason_codes`, and `warnings` before
interpreting `data`; unavailable evidence is never presented as zero or
healthy.

## Stop or Revoke Access

Use **System > Operations MCP** to stop serving without deleting credentials.
To remove the final token, stop MCP first and then revoke it. A stopped MCP
returns a stable unavailable response on every Manager node.

Stopping also writes a 30-second pprof transition fence. This lets an
already-started maximum-duration capture finish before a different owner can
start another cluster-wide profile.
