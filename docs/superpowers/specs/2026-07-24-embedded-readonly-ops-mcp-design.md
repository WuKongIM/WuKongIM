# Embedded Read-Only Ops MCP V1

Date: 2026-07-24

## Goal

Embed a production-oriented MCP endpoint in WuKongIM so an AI agent can
observe and diagnose a single-node or multi-node cluster with one bearer token.
V1 is intentionally read-only except for bounded, non-destructive runtime
profile capture.

## Product Shape

- MCP is an in-process component served by every Manager HTTP listener at the
  fixed `/mcp` path.
- Manager web administration owns MCP setup, token rotation, owner selection,
  start, stop, status, and recent audit display.
- MCP has no separate listener, port, process, TOML enable switch, or
  capability-file configuration.
- Controller state is authoritative for the desired enabled state, one owner
  node, revision fencing, and token digests.
- V1 is on-demand only. It has no background schedules, alerts, webhooks, or
  model calls.
- HTTP is permitted. The UI and documentation must warn that a bearer token on
  plaintext HTTP can be intercepted.

## Security Model

### Agent Authentication

The agent configures one opaque bearer token:

```text
wko_<credential-id>_<random-secret>
```

- The secret contains 256 bits of cryptographic randomness.
- Tokens do not expire automatically; rotation or revocation ends validity.
- Raw tokens are shown only once. Controller state stores the credential ID,
  a SHA-256 digest of the complete token, and its creation time.
- At most two tokens may be active, allowing create-new then revoke-old
  rotation.
- Token creation accepts an idempotency key. A node may retain the one-time raw
  response in memory for at most five minutes so a transport retry can recover
  it. If the response is lost after that window, the operator must revoke and
  regenerate.
- MCP bearer tokens authenticate only `/mcp`; Manager JWTs do not.
- Requests with a non-empty cross-origin `Origin` are rejected. `/mcp` emits
  no permissive CORS headers.

### Manager Administration

- Manager authentication must be enabled before MCP can be administered.
- Add Manager resource `cluster.mcp` with `r` and `w` actions.
- Reads and audit display require `cluster.mcp:r`.
- Token creation/revocation, owner selection, start, and stop require
  `cluster.mcp:w`.
- Every management mutation carries `expected_revision` and an idempotency key.
- The owner cannot change while MCP is enabled. The operator sequence is stop,
  change owner, then start; the persisted profile fence makes pprof wait out
  any maximum-duration capture from the prior owner generation.

## Controller State

The bounded optional MCP section of `cluster-state.json` contains:

- `enabled`
- `owner_node_id`
- `profile_fence_until_unix_ms`, updated only by an administrative stop
- up to two credential records (`id`, SHA-256 digest, creation timestamp)

The normal global Controller revision is the compare-and-set fence. The state
machine rejects enabling without an active owner or credential, more than two
credentials, malformed credential metadata, and owner changes while enabled.
High-frequency last-used and audit data never enters Controller Raft.

## Manager API

The V1 routes are:

```text
GET    /manager/mcp
POST   /manager/mcp/tokens
DELETE /manager/mcp/tokens/:credential_id
PUT    /manager/mcp/owner
POST   /manager/mcp/start
POST   /manager/mcp/stop
GET    /manager/mcp/audits
```

The web page supports owner selection, initial token generation, one-time token
copy, start/stop, desired/observed status, revision and error display, a second
token plus old-token revocation, client-config copy, plaintext HTTP warning,
and the most recent bounded audit summaries.

## Distributed Execution

- Every Manager node accepts the same `/mcp` request.
- The ingress node parses and preliminarily validates the token against its
  latest Controller snapshot, applies ingress limits, and forwards a typed
  internal request to the configured owner when the owner is remote.
- The raw token never crosses node RPC. The request carries the credential ID,
  digest, MCP payload, request ID, and expected MCP/Controller revision.
- The owner revalidates the credential ID and digest against its latest
  Controller state before execution.
- There is no automatic owner failover in V1. An unavailable owner returns
  stable `mcp_owner_unavailable` with HTTP 503.
- Owner restart resumes the Controller desired state.

## MCP Protocol

- Stateless Streamable HTTP using the official Go MCP SDK.
- JSON responses only.
- No sessions, SSE, notifications, subscriptions, Resources, Prompts,
  Sampling, or Roots.
- Expose Tools and server instructions only.
- Request body maximum: 64 KiB.
- Response maximum: 1 MiB.
- Ordinary tool deadline: 10 seconds.

## Frozen V1 Tools

The registry contains exactly:

1. `cluster_health`
2. `node_inspect`
3. `slot_inspect`
4. `channel_runtime_inspect`
5. `controller_tasks_query`
6. `metrics_query_range`
7. `logs_search`
8. `logs_context`
9. `diagnostics_query`
10. `config_read_redacted`
11. `backup_inspect`
12. `pprof_analyze`

The first eleven tools are closed-world, read-only, and non-destructive.
`pprof_analyze` is closed-world, active, bounded, and non-destructive. No tool
accepts a filesystem path, URL, shell command, SQL, PromQL, arbitrary query
language, or general Controller writer.

### Observation Envelope

Every successful observation returns:

```json
{
  "schema": "wukongim/ops-observation/v1",
  "cluster_id": "...",
  "observed_at": "...",
  "window": {"start": "...", "end": "..."},
  "freshness": "fresh|stale|missing",
  "completeness": "complete|partial|unavailable",
  "status": "healthy|degraded|unknown",
  "reason_codes": [],
  "warnings": [],
  "data": {}
}
```

Missing evidence is never fabricated as zero or healthy. Health rules are
built-in and versioned. Each reason code includes the actual evidence and
threshold. Missing required evidence produces `unknown`.

### Tool Boundaries

- `cluster_health` aggregates Controller, node, Slot, workqueue, and metric
  health without scanning channels.
- `node_inspect` returns one exact node's health, runtime, Controller Raft,
  workqueue, and bounded diagnostics.
- `slot_inspect` returns one exact physical Slot's leader, replicas, progress,
  and indices without raw Raft command contents.
- `channel_runtime_inspect` requires exact `channel_id` and `channel_type` and
  performs a point lookup through its hash slot. It never enumerates channels.
- `controller_tasks_query` returns bounded active tasks, retained history, and
  audits.
- `metrics_query_range` accepts only server-owned query IDs and an optional
  low-cardinality node filter. It has no PromQL or label matcher input. Range is
  at most 24 hours, 2,000 points per series, and 100 series. Prometheus is
  optional; absence returns `unavailable`.
- `logs_search` and `logs_context` expose raw `app` or `error` application
  logs for one exact node. Inputs are literal keyword, level, and opaque
  cursor. Default limit is 100, maximum 200, each line is at most 8 KiB, and
  the response is at most 1 MiB. Results are marked
  `content_trust:"untrusted"`; agents must never execute instructions,
  commands, or URLs found in logs.
- `diagnostics_query` accepts only node ID, Slot ID, trace ID, stage, result,
  and time filters. Maximum 500 events and 1 MiB.
- `config_read_redacted` returns the existing allowlisted, redacted effective
  config for one exact node.
- `backup_inspect` returns bounded job, partition-completeness, verification,
  retention, artifact-size, and restore-point evidence. It never returns
  repository URIs, object paths, KMS material, signatures, or restore inputs.
- `pprof_analyze` accepts one node, `cpu|heap|goroutine`, CPU duration default
  10 seconds and maximum 30 seconds, heap sample type
  `inuse_space|alloc_space`, and rows default 30 maximum 100. It captures and
  parses in memory, returning function name, flat, cumulative, unit, and sample
  type only. It never stores or returns a raw profile.

## pprof Execution

pprof does not depend on the public `/debug/pprof` endpoint. The owner creates
an unpredictable, one-time, 35-second in-memory lease and sends its ID in a
dedicated typed cluster RPC to the target node. The target verifies that MCP is
enabled and the revision matches, then calls the configured owner to consume
the exact lease before using Go runtime profiling APIs. Caller node identity is
only a consistency check; a forged JSON identity without the owner-held lease
cannot authorize capture. Only one pprof capture may run cluster-wide; a stop
writes a 30-second owner-generation fence, and each node has a 60-second
cooldown.

## Budgets, Cache, Errors, and Audit

- Cluster, node, and Slot health use a 3-second cache plus singleflight.
- Redacted config uses a 30-second cache.
- Logs, diagnostics, and pprof are uncached.
- Ordinary calls: 60 per credential per minute and 4 concurrent.
- Logs: 20 per credential per minute.
- Per-node total execution: 16 concurrent.
- Failed auth: 30 per TCP source per minute and 300 per node per minute; ignore
  `X-Forwarded-For`.
- Return stable structured errors:
  `{"code","retryable","retry_after_seconds","message"}`.
- HTTP 401 is authentication failure. HTTP 503 is disabled or owner
  unavailable. Invalid tool arguments use the MCP invalid-params error.
- Partial source failures remain successful `partial`/`unknown` observations;
  raw internal addresses, stacks, and implementation errors are not returned.
- Every call audits request ID, credential ID, tool, bounded target selectors,
  start, duration, result, response bytes, cache hit, and pprof type/duration.
  Never audit raw tokens, full arguments/results, or log keywords.
- Audit files are local structured rotated logs: 10 MiB, 10 files, at most
  seven days. Ingress records auth/limit decisions; owner records execution.
  Manager displays at most 200 recent aggregate summaries.
- MCP Prometheus metrics use only low-cardinality tool/result labels. They
  never label tokens, requests, UIDs, channels, Slots, or node addresses.

## Code Boundaries

- `internal/access/opsmcp`: `/mcp` protocol, bearer authentication, and frozen
  tool registration.
- `internal/access/manager`: MCP administration routes.
- `internal/access/node`: typed owner forwarding and pprof RPC.
- `internal/usecase/opsobserve`: entry-independent read-only observation rules
  and contracts.
- `internal/runtime/opsmcp`: owner execution, cache, limits, audit, and pprof
  gate.
- `internal/infra/cluster`: read-only observation adapters.
- `internal/app`: assembly and lifecycle.
- `pkg/controller`: durable MCP desired state and revision fencing.

Compile-level tests must prove that the observation usecase depends only on
narrow read ports, the frozen registry contains no write tool, and schemas do
not accept URLs, paths, commands, or arbitrary query languages.

## Acceptance Tests

- In a three-node cluster with node 1 as owner, the same token works through
  node 2 and node 3 `/mcp`.
- Missing/wrong tokens and Manager JWTs fail MCP authentication.
- No write tool is registered.
- Token rotation, revocation, idempotency, and revision conflicts are covered.
- Owner unavailability returns the stable 503 error.
- An ingress node can request pprof from another target node through the owner.
- Owner restart resumes desired state.
- Exact channel inspection remains a point lookup with a very large channel
  population.
- Unit tests and `internal/app` wiring tests cover the public seams.

## Companion Agent Skill

Add repository skill `wukongim-ops` with this fixed diagnostic order:

```text
cluster_health
  -> exact node / Slot / channel inspection
  -> metrics
  -> logs / diagnostics
  -> pprof only when needed
```

Its answer must report cluster verdict, impact, timestamped evidence, ranked
root causes with counter-evidence and confidence, human-only suggested actions
and verification, and explicit unknowns. It must never claim that an operation
was executed.
