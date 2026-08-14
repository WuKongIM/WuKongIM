---
scope: package
summary: Adapts product, benchmark, debug, and compatibility HTTP requests to internal use cases.
---

# internal/access/api Flow

## Responsibility

This package owns the product HTTP listener, route registration, request and
response DTOs, CORS, entry validation, legacy-compatible envelopes, and the
embedded chat Demo. It adapts HTTP requests to entry-independent use cases and
runtime ports; it does not own message, membership, conversation, channel, or
user business state.

## Boundaries

The composition root supplies message, CMD sync, conversation, channel, user,
benchmark, top, diagnostics, metrics, and debug ports. Optional capabilities
remain explicit: their routes are absent or fail closed when the matching port
is not wired. Business ordering, cursor semantics, durable writes, Channel
authority, and compatibility decisions below the HTTP envelope stay in the
corresponding use case or runtime.

## Main Flows

```text
product HTTP request
  -> maintenance and request-shape gates
  -> legacy/canonical DTO mapping
  -> message, CMD, conversation, channel, or user use case
  -> stable HTTP envelope

bench or debug request
  -> feature-enabled and bearer-capability gates
  -> bounded runtime/read-model port
  -> low-cardinality response or stable failure

/demo/*
  -> embedded immutable asset or revalidated index
  -> same-origin product APIs and /route discovery
```

## Invariants and Failure Semantics

- Controller restore maintenance rejects product, route-discovery, and bench
  handlers with stable `503 maintenance` before business work. Health,
  readiness, metrics, debug, top, and Demo assets remain available.
- A configured `bench.api_token` protects every `/bench/v1/*` and enabled
  `/debug/*` route. An empty token is controlled-environment compatibility, not
  a production authentication claim.
- `/route`, legacy channel/user/message/CMD/conversation routes, and their
  response envelopes remain compatibility surfaces independent of bench mode.
- Person-channel IDs are normalized only at the entry boundary; durable
  membership, opaque cursors, badge floors, and Channel reads stay below it.
- Benchmark mutation routes write only through the supplied benchmark data
  port. Missing mutation capability returns an explicit unsupported result.
- Debug failures and observations never expose raw requested identities or
  unbounded internal errors. Metrics must not add UID or Channel labels.
- The adapter never writes storage, resolves distributed authority, or performs
  post-commit effects directly.

## Read First

- [server.go](server.go)
- [message_send.go](message_send.go)
- [conversation_list.go](conversation_list.go)
- [bench_runtime.go](bench_runtime.go)
- [debug.go](debug.go)

## Update Triggers

- Route registration, compatibility envelopes, or entry validation change.
- Maintenance, bench/debug authentication, or optional-capability gates change.
- Person-channel conversion, cursor projection, or error mapping moves layers.
- The adapter gains a new state mutation or distributed-authority dependency.
