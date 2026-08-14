---
scope: package
summary: Authenticates and routes the embedded closed-world Operations MCP while revalidating owner, revision, credential, and admission fences.
---

# Operations MCP Access Flow

## Responsibility

`internal/access/opsmcp` hosts the stateless JSON Streamable HTTP Operations MCP
on every Manager listener, authenticates dedicated credentials, forwards to the
selected owner when needed, and delegates the frozen observation tools.
It does not define observation semantics, desired state, or runtime profiling policy.

## Boundaries

- Manager JWTs and browser origins are not MCP credentials.
- The adapter exposes no Resources, Prompts, Sampling, Roots, SSE, arbitrary
  query, or write operation.
- Tool behavior belongs to `opsobserve`; owner selection and desired state are
  Controller-authoritative.

## Main Flows

1. Reject Origin, bound the body, create a server correlation ID, and verify one
   `wko_*` credential against current desired state.
2. Execute locally on the owner or forward bounded JSON plus credential digest
   and expected revision; the receiver revalidates every fence.
3. Strictly decode an allowlisted tool request and return its bounded observation
   or stable public error.

## Invariants and Failure Semantics

- Every Manager listener mounts the same endpoint; no extra process, port, or
  configuration switch exists.
- Unknown fields/methods, malformed/oversized input, rate/concurrency rejection,
  disabled state, and unavailable owner fail with stable non-secret errors.
- Forwarding carries credential identity/digest, never the raw token.
- The tool registry remains frozen and observation-only.

## Read First

- [MCP handler](handler.go)
- [Handler tests](handler_test.go)

## Update Triggers

Update this file when authentication, forwarding, revalidation, tool inventory,
HTTP exposure, admission bounds, or public error mapping changes.
