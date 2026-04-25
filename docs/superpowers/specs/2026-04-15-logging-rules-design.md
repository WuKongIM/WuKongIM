# Logging Rules Design

**Date:** 2026-04-15
**Status:** Proposed

## Overview

Define a repository-wide logging rule set that lets engineers identify the responsible module and likely code location from a single `warn` or `error` line, while still supporting future structured-log indexing.

The immediate operational target is text-first troubleshooting with `tail` and `grep`. The design therefore optimizes for two things at the same time:

- a human can understand the failure by reading `module + msg`
- operators can reliably grep by stable field values such as `event`, `channelID`, `slotID`, `nodeID`, or `traceID`

The design keeps the current logger abstraction in `pkg/wklog` and the current zap-based implementation in `internal/log`. It adds naming rules, field rules, severity rules, and a small helper layer in `pkg/wklog` to stop key drift.

## Goals

- Make a single `warn` or `error` line enough to identify the owning layer and likely package
- Keep logs readable in plain text when the main investigation tools are `tail` and `grep`
- Preserve structured fields so the same logs remain useful after moving to Loki, ELK, SLS, or similar systems
- Standardize module naming, event naming, object identifiers, and severity boundaries across `internal/*` and `pkg/*`
- Ensure one failure chain has one primary `Error` owner instead of repeated duplicate errors at every layer
- Minimize migration cost by extending the existing `pkg/wklog` API instead of replacing it

## Non-Goals

- Replacing the current `wklog.Logger` interface
- Introducing a heavy logging DSL or mandatory builder API
- Requiring a global event registry before the first rollout
- Making every success path emit logs
- Converting the entire repository to the new style in one cut
- Solving tracing, metrics, and audit logging as part of this change

## Current Foundation

The repository already has the right base primitives for this design:

- `pkg/wklog/logger.go` exposes `Named(name string)` and `With(fields ...Field)`
- `internal/log/zap.go` maps `Named(...)` to the structured `module` field and includes caller information
- `internal/app/build.go` already injects top-level named loggers such as `cluster`, `message`, `presence`, `access.api`, and `access.gateway`

This means module ownership already exists in the composition root. The missing piece is consistent refinement below the top level and consistent field semantics at call sites.

## Problem Statement

Today the logger plumbing is present, but the repository does not yet enforce a unified rule set for:

- how deep module names should go
- how to describe actions consistently
- which object identifiers must be logged for each failure
- which layer owns the primary `Error`
- which keys should be reused instead of re-invented per package

Without those rules, logs tend to degrade into one of two bad states:

1. logs identify only a large area such as `message` or `cluster`, but not the failing submodule or object
2. logs contain ad hoc text and ad hoc keys, which makes `grep` and later structured search unreliable

The design goal is not “more logs”. The goal is “one log line can answer responsibility, action, object, and outcome”.

## Design Principles

### 1. Module first, object second, stacktrace last

Operators should first learn which module owns the failure, then which concrete object is affected, and only then inspect deeper stack details if needed.

### 2. `msg` is for humans, fields are for operators and machines

`msg` must be readable as a short action-result sentence. Dynamic values belong in fields, not hidden inside prose.

### 3. `warn` and `error` must be grep-stable

Every important abnormal line must carry a stable `event` token and stable object keys so recurring incidents can be found with simple text search.

### 4. One failure chain has one primary `Error`

The layer that actually owns the failed action logs the main `Error`. Upstream layers may add context, but should not duplicate the same failure as another primary `Error` unless they own a separate failure.

### 5. Prefer light guardrails over a heavy framework

The repository should standardize the 80 percent case through helper fields and naming rules, not through a complicated mandatory logging DSL.

## Primary Localization Model

Every important abnormal log line should answer these five questions:

1. Which layer or module failed? → `module`
2. Which action failed? → `event`
3. Which object or instance failed? → object identifier fields
4. Was it a recoverable degradation or a hard failure? → `level`
5. Can the same request or workflow be followed across layers? → `traceID` or `requestID`

The canonical mental model is:

```text
module → event → object fields → outcome/error
```

Example:

```text
ERROR [message.send] persist committed message failed event=message.send.persist.failed channelID=u1@u2 channelType=1 messageID=89123 uid=u1 traceID=gw-9f2c error="raft quorum unavailable"
```

From one line, the operator can immediately see:

- owner: `message.send`
- action: committed-message persistence during send
- object: `channelID`, `messageID`, `uid`
- chain: `traceID`
- failure: `raft quorum unavailable`

## Log Entry Shape

A good log line has three layers of information:

1. fixed header from the logger backend
   - timestamp
   - level
   - `module`
   - caller
2. short human-readable message
   - `msg`
3. stable searchable fields
   - `event`
   - object identifiers
   - chain identifiers
   - process control fields
   - `error`

Target shape:

```text
2026-04-15 21:03:11.245 ERROR [message.send] persist committed message failed event=message.send.persist.failed channelID=person:u1@u2 channelType=1 messageID=89123 uid=u1 traceID=gw-9f2c duration=12ms error="raft quorum unavailable"
```

## Canonical Module Naming

### Rule

`module` uses dotted hierarchical names and is derived only by chaining `Named(...)` from an existing parent logger.

### Naming form

```text
<top-level>.<submodule>[.<submodule>...]
```

### Examples

- `access.gateway.conn`
- `access.gateway.frame`
- `access.api.http`
- `access.node.rpc`
- `message.send`
- `message.retry`
- `presence.activate`
- `presence.authority`
- `runtime.delivery.retry`
- `cluster.transport`
- `cluster.controller`
- `cluster.reconcile`
- `slot.multiraft`
- `channel.replica`

### Rules

- top-level ownership comes from `internal/app/build.go`
- deeper names are created only by child modules from the injected logger
- do not invent unrelated aliases such as `msg.send`, `cluster_rpc`, or `gatewayMsg`
- do not encode object identifiers into the module name; identifiers belong in fields

## Event Naming

### Rule

Every `Warn` or `Error` must include an `event` field.

### Format

```text
<module>.<action>.<result>
```

### Examples

- `access.gateway.conn.auth_failed`
- `access.gateway.frame.send_failed`
- `message.send.persist.failed`
- `message.send.dispatch.succeeded`
- `presence.authority.lookup.timeout`
- `cluster.transport.rpc.timeout`
- `cluster.reconcile.task.failed`

`event` intentionally overlaps with `module`. This is desirable for plain-text troubleshooting:

- `module` is easy for people to scan
- `event` is easy to grep exactly

## Message Style

### Rule

`msg` must be a short “action + result” sentence. It should still make sense without reading the fields.

### Good examples

- `persist committed message failed`
- `forward rpc timed out, retrying`
- `session activated`
- `channel metadata refresh skipped`

### Bad examples

- `message error`
- `something wrong`
- `process failed`
- `got exception`

### Guidance

- keep `msg` readable and short
- do not hide identifiers inside prose if they can be fields
- do not use `msg` as the only source of action identity; `event` remains mandatory for abnormal logs

## Canonical Field Dictionary

Fields are divided into four groups.

### 1. Common localization fields

Used broadly across important logs:

- `event`
- `traceID`
- `requestID`
- `sourceModule`
- `duration`

### 2. Business object fields

Used when the abnormality is tied to a user, connection, channel, or message:

- `uid`
- `fromUID`
- `toUID`
- `sessionID`
- `connID`
- `gatewayBootID`
- `channelID`
- `channelType`
- `messageID`
- `clientMsgNo`
- `streamNo`

### 3. Cluster and replication fields

Used when the abnormality is tied to nodes, slots, replication, or metadata versions:

- `nodeID`
- `targetNodeID`
- `leaderNodeID`
- `peerNodeID`
- `slotID`
- `term`
- `index`
- `tableVersion`
- `epoch`
- `leaderEpoch`

### 4. Process control and retry fields

Used when the abnormality reflects a transient state, timeout, retry, or fallback:

- `attempt`
- `maxAttempts`
- `timeout`
- `reason`

### Error field

Failures must always use the canonical `error` key through `wklog.Error(err)`.

An optional `errorKind` field may be added later for broad classes such as `timeout`, `not_found`, `not_leader`, or `conflict`, but the initial rollout does not require a global classification system.

## Severity Rules

### `Debug`

Use for high-frequency internal mechanics and branch detail.

Typical examples:

- route selection result
- retry attempt details
- local state transitions
- scheduler or mailbox decisions

`Debug` is not the primary production observability layer.

### `Info`

Use for visible normal lifecycle boundaries and selected business-success boundaries.

Typical examples:

- component start/stop
- listener registered
- leader changed
- session activated
- major batch sync completed

Do not emit `Info` for every successful hot-path operation.

### `Warn`

Use when the system degrades, retries, falls back, or observes an abnormal but recoverable state.

Typical examples:

- RPC timeout with retry pending
- stale metadata detected, refresh triggered
- controller temporarily unreachable, local fallback engaged
- request rejected by policy or missing authentication

### `Error`

Use when the owned action fails and the expected result was not achieved.

Every `Error` must include:

- `event`
- `error`
- at least one primary object field

If the failure crosses layers, the main `Error` belongs to the responsibility owner.

### `Fatal`

Reserve for process-level failure where the node cannot continue.

Expected boundary:

- application startup and composition root
- process entrypoint

Business modules, usecases, and runtimes should not use `Fatal` for ordinary request failures.

## Primary Error Ownership Rule

One failure chain should have one primary `Error` owner.

### Owner mapping

- entry protocol rejection or parse failure → `access.*`
- business workflow failure → `internal/usecase/*`
- local runtime mechanism failure → `internal/runtime/*`
- distributed runtime, storage, transport, or replication failure → responsible `pkg/*` module

### Upstream behavior

When a child module already emitted the primary `Error`, an upstream caller should usually:

- return the error without logging, or
- log a `Warn` or `Debug` with added chain context using `sourceModule`

### Why

Without this rule, a single incident becomes four different `error` lines from `access`, `usecase`, `runtime`, and `pkg`, which makes the first failing layer harder to identify.

## Layer-Specific Logging Boundaries

### `internal/access/*`

Responsibility:

- connection/session boundaries
- protocol parse/validation/auth failures
- request admission or rejection
- request timeout observed at the entry boundary

Should log:

- session open/close lifecycle boundaries
- authentication accepted/rejected
- malformed frame or invalid request payload
- entry-side timeout or policy rejection

Should not log as primary `Error`:

- downstream business failure already owned by `message`, `presence`, or other usecases

Recommended modules:

- `access.gateway.conn`
- `access.gateway.frame`
- `access.api.http`
- `access.node.rpc`

### `internal/usecase/*`

Responsibility:

- business workflow execution
- final business success/failure outcome
- business-level branching and orchestration

Should log:

- primary failure of send, ack, sync, dispatch, or activation workflows
- meaningful boundary success events when useful for operations
- high-value retry or fallback decisions

Recommended modules:

- `message.send`
- `message.recvack`
- `message.retry`
- `presence.activate`
- `presence.authority`
- `conversation.sync`
- `delivery.dispatch`
- `user.token`

### `internal/runtime/*`

Responsibility:

- local node mechanics such as registries, mailboxes, retry workers, ID allocation, and local routing

Should log:

- local enqueue/dequeue failures
- mailbox backpressure
- retry scheduler activity
- local registration conflicts or local runtime corruption signals

Should not log:

- business failure phrased as if the runtime owned the workflow end-to-end

Recommended modules:

- `runtime.online.registry`
- `runtime.delivery.mailbox`
- `runtime.delivery.retry`
- `runtime.messageid`
- `runtime.sequence`

### `pkg/*`

Responsibility:

- cluster transport
- controller and planner behavior
- slot and multiraft runtime
- channel replication and storage
- transport and RPC infrastructure

Should log:

- node/slot/peer/term/index-oriented failures
- transport and replication timeouts
- reconciliation or state-machine failures
- storage persistence failures

Recommended modules:

- `cluster.transport`
- `cluster.controller`
- `cluster.reconcile`
- `slot.multiraft`
- `slot.fsm`
- `controller.raft`
- `controller.plane`
- `channel.replica`
- `transport.rpc`

## `pkg/wklog` Light Extension Design

The existing `Logger` interface remains unchanged.

The first rollout adds small field helpers only, to stop key drift and make common call sites easier to review.

### Proposed field helpers

```go
wklog.Event("message.send.persist.failed")
wklog.TraceID("gw-9f2c")
wklog.RequestID("req-123")
wklog.SourceModule("message.send")

wklog.NodeID(1)
wklog.TargetNodeID(3)
wklog.LeaderNodeID(2)
wklog.PeerNodeID(4)

wklog.SlotID(8)
wklog.ChannelID("u1@u2")
wklog.ChannelType(1)
wklog.MessageID(89123)

wklog.UID("u1")
wklog.SessionID(812)
wklog.ConnID(991)

wklog.Attempt(3)
wklog.Timeout(2 * time.Second)
wklog.Reason("metadata stale")
```

### Optional grouped helpers

These may be added if they improve call-site readability without hiding the field keys:

```go
wklog.Channel(channelID, channelType) // returns []Field
wklog.Peer(nodeID, targetNodeID)      // returns []Field
```

### Explicitly deferred

The first rollout does not require:

- a mandatory builder pattern
- a global enum of all event names
- complex error-class taxonomies
- automatic duplicate suppression machinery

## Canonical Templates

### Success boundary

```go
logger.Info(
    "session activated",
    wklog.Event("presence.activate.succeeded"),
    wklog.UID(uid),
    wklog.SessionID(sessionID),
    wklog.NodeID(localNodeID),
)
```

### Recoverable abnormality

```go
logger.Warn(
    "forward rpc timed out, retrying",
    wklog.Event("cluster.transport.rpc.timeout"),
    wklog.NodeID(localNodeID),
    wklog.TargetNodeID(targetNodeID),
    wklog.SlotID(slotID),
    wklog.Attempt(attempt),
    wklog.Timeout(timeout),
    wklog.Error(err),
)
```

### Primary failure

```go
logger.Error(
    "persist committed message failed",
    wklog.Event("message.send.persist.failed"),
    wklog.ChannelID(channelID),
    wklog.ChannelType(channelType),
    wklog.MessageID(messageID),
    wklog.UID(uid),
    wklog.TraceID(traceID),
    wklog.Error(err),
)
```

### Upstream contextual log

```go
logger.Warn(
    "send request failed",
    wklog.Event("access.gateway.frame.send_failed"),
    wklog.SourceModule("message.send"),
    wklog.SessionID(sessionID),
    wklog.TraceID(traceID),
    wklog.Error(err),
)
```

## Rollout Plan

### Phase 1: foundation

- add helper fields to `pkg/wklog`
- document the naming and ownership rules in repository docs
- keep the current zap backend unchanged

### Phase 2: key path migration

Apply the new style first to the highest operational value paths:

- `access.gateway`
- `message`
- `presence`
- `cluster.transport`

### Phase 3: distributed runtime migration

Expand to:

- `cluster.controller`
- `cluster.reconcile`
- `slot.multiraft`
- `channel.replica`

### Phase 4: opportunistic completion

When other modules change for feature work or bugfixes, migrate them to the new field and ownership rules instead of doing a repository-wide mechanical sweep immediately.

## Review Checklist

A new `Warn` or `Error` log is acceptable only if review can answer all of the following quickly:

- Does `module` point to a clear owning package or submodule?
- Does `msg` describe the action and result without reading the fields?
- Is `event` present and grep-stable?
- Is there at least one concrete object identifier?
- If the log is a primary failure, is this the right ownership layer?
- If the log is an upstream contextual line, did it avoid duplicating a child `Error`?
- Are canonical field keys reused instead of introducing aliases like `userID`, `userId`, and `uid` together?

## Acceptance Criteria

The design is considered successfully adopted when the following are true on migrated paths:

- a random `Warn` or `Error` line maps directly from `module` to a likely repository directory
- a random `Warn` or `Error` line communicates action and outcome from `module + msg`
- important failures always include at least one object identifier field
- duplicate primary `Error` lines across one failure chain are significantly reduced
- operators can reliably `grep` by `event` or `module`
- key names for the same concepts stop drifting across packages

## Risks

### 1. Over-logging

If the repository starts emitting `Info` on every hot-path success, operators will lose the signal.

Mitigation:

- keep `Info` limited to boundaries and major transitions
- keep hot-path detail in `Debug`

### 2. Under-specified object fields

If teams follow the module naming rules but forget object identifiers, logs still localize only to the package, not the failing instance.

Mitigation:

- require at least one primary object field on every `Warn` and `Error`
- prioritize helper functions for the most common identifiers

### 3. Duplicate ownership in layered call chains

Without discipline, both the child and parent layers may still log the same failure as `Error`.

Mitigation:

- adopt the single-owner `Error` rule in code review
- use `sourceModule` when parents need to add context

### 4. Event naming drift

If event names vary between similar modules, grep becomes less reliable.

Mitigation:

- require the `<module>.<action>.<result>` form
- favor helper-backed event fields even if event strings remain free-form initially

## Summary Rule

The repository should follow one summary rule:

> First use `module` to assign responsibility, then use `event` to identify the action, then use object fields to identify the instance. `msg` is for people; fields are for grep and machines; a failure chain has one primary `Error` owner.
