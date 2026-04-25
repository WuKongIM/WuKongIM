# Access / Usecase / Runtime Design

## Overview

Replace the current `internal/service` package with a clearer long-term structure built around:

- `internal/access` for ingress adapters
- `internal/usecase` for reusable application use cases
- `internal/runtime` for node-local runtime primitives
- `internal/app` for process composition

The current `internal/service` package started as a gateway-facing business runtime, but its name and shape no longer match its actual responsibility. It currently mixes gateway lifecycle adaptation, frame routing, business use cases, and node-local online-session runtime. That structure will become a coupling hotspot as soon as new ingress paths such as API, RPC, or background tasks are added.

This redesign is intentionally non-compatible. The goal is not to preserve the old package boundary. The goal is to replace it with a structure that remains stable as ingress surfaces grow.

## Goals

- Eliminate `internal/service` as a catch-all application layer
- Keep ingress-specific concerns in thin adapter packages
- Make business use cases reusable across `gateway`, `api`, `rpc`, and `task`
- Separate node-local runtime primitives from business use cases
- Keep `internal/app` as the only composition root
- Make future ingress expansion additive instead of structural

## Non-Goals

- Preserving public compatibility for `internal/service`
- Introducing heavy DDD ceremony such as `domain/entity/repository/factory`
- Building all future ingress adapters immediately
- Solving full remote delivery semantics in this refactor

## Problem Statement

Today `internal/service` contains four different concerns:

1. gateway handler lifecycle adaptation
2. frame routing and wire-model branching
3. message use-case logic
4. online session registry and local delivery runtime

That is manageable while only one ingress exists, but it does not scale. If HTTP API, RPC, or scheduled tasks are added, there are only two likely outcomes:

- more ingress-specific code gets piled into `internal/service`, or
- parallel `api service`, `rpc service`, and `task service` packages emerge and duplicate use-case logic

Both outcomes are structurally weak.

## Recommended Structure

```text
internal/
  app/
    app.go
    build.go
    lifecycle.go
    config.go

  access/
    gateway/
      handler.go
      frame_router.go
      mapper.go
      lifecycle.go

    api/
      ...

    rpc/
      ...

    task/
      ...

  usecase/
    message/
      app.go
      command.go
      result.go
      send.go
      recvack.go
      deps.go

    session/
      app.go
      bind.go
      unbind.go
      query.go
      deps.go

    user/
      app.go
      deps.go

    channel/
      app.go
      deps.go

  runtime/
    online/
      registry.go
      delivery.go
      types.go

    sequence/
      allocator.go
```

The first implementation step only needs:

- `internal/access/gateway`
- `internal/usecase/message`
- `internal/runtime/online`
- `internal/runtime/sequence`

Empty future ingress directories should not be created until needed.

## Layer Responsibilities

### `internal/access`

Ingress adapters only.

Responsibilities:

- accept ingress-specific payloads and callback contexts
- map wire or transport models into use-case commands
- invoke use-case apps
- map use-case results back into ingress-specific responses

Non-responsibilities:

- owning reusable business rules
- holding persistent business state
- knowing how storage or cluster dependencies are assembled

### `internal/usecase`

Application use cases only.

Responsibilities:

- enforce business invariants
- orchestrate stores, cluster ports, and runtime ports
- expose ingress-agnostic command/result contracts

Non-responsibilities:

- understanding `gateway.Context`
- understanding HTTP request/response objects
- directly writing protocol frames

### `internal/runtime`

Node-local runtime primitives only.

Responsibilities:

- online connection registry
- local realtime delivery
- local sequence allocation

These are process runtime capabilities, not business domains and not ingress adapters.

### `internal/app`

Process composition only.

Responsibilities:

- construct runtime collaborators
- construct use-case apps
- construct ingress adapters
- wire them into gateway, storage, and cluster components

## Dependency Rules

The allowed dependency direction is:

```text
access/*  -> usecase/*, runtime/*
usecase/* -> runtime/*, pkg/*
runtime/* -> internal/gateway/session (only when needed), pkg/*
app/*     -> access/*, usecase/*, runtime/*, pkg/*
```

Hard rules:

- `usecase/*` must not import `internal/gateway`
- `usecase/*` must not import `net/http`
- `usecase/*` must not accept `wkpacket` wire objects as primary command inputs
- `app/*` remains the only composition root
- no replacement global `Service` aggregate is allowed

## Core Design Decisions

### 1. Delete `internal/service`

The name is no longer accurate and encourages future misuse. This package should be removed after the migration lands.

### 2. Remove the monolithic `Service` aggregate

Do not replace `internal/service` with another fat aggregate object.

Instead, assemble explicit use-case apps such as:

```go
type MessageApp struct {
    identities IdentityStore
    channels   ChannelStore
    online     OnlineRegistry
    delivery   Delivery
    sequence   SequenceAllocator
    clock      Clock
}

type SessionApp struct {
    online OnlineRegistry
    clock  Clock
}
```

### 3. Treat gateway as an adapter, not the core runtime

`gateway.Handler` implementation should live in `internal/access/gateway`. It should translate:

- session open/close events into session use-case calls
- inbound `wkpacket` frames into message commands
- message results into `wkpacket` reply frames

### 4. Rename session runtime concepts

Current names such as `SessionRegistry` and `SessionMeta` are too coupled to gateway vocabulary.

Preferred names:

- `SessionRegistry` -> `OnlineRegistry`
- `SessionMeta` -> `OnlineConn`
- `SessionsByUID` -> `ConnectionsByUID`

This makes the runtime model reusable for API-side online queries and future push flows.

### 5. Use explicit command/result models

Ingress adapters should translate wire objects into use-case commands.

Example:

```go
type SendCommand struct {
    SenderUID   string
    ChannelID   string
    ChannelType uint8
    Payload     []byte
    ClientSeq   uint32
    ClientMsgNo string
    StreamNo    string
    Topic       string
}

type SendResult struct {
    MessageID  int64
    MessageSeq uint32
    Reason     uint8
}
```

This keeps message use cases reusable across gateway and API paths.

## Mapping From Current Files

| Current path | New home |
|---|---|
| `internal/service/handler.go` | `internal/access/gateway/handler.go` |
| `internal/service/frame_router.go` | `internal/access/gateway/frame_router.go` |
| `internal/service/send.go` | `internal/usecase/message/send.go` plus `internal/access/gateway/mapper.go` |
| `internal/service/recvack.go` | `internal/usecase/message/recvack.go` |
| `internal/service/registry.go` | `internal/runtime/online/registry.go` |
| `internal/service/session_state.go` | split between `internal/runtime/online/types.go` and `internal/access/gateway/lifecycle.go` |
| `internal/service/delivery.go` | `internal/runtime/online/delivery.go` |
| `internal/service/sequence.go` | `internal/runtime/sequence/allocator.go` |
| `internal/service/options.go` | split into `deps.go` files beside each use case |
| `internal/service/service.go` | deleted |

## First Migration Slice

The first migration slice should do only this:

1. create `runtime/online`
2. create `runtime/sequence`
3. create `usecase/message`
4. create `access/gateway`
5. rewire `internal/app`
6. delete `internal/service`

Do not introduce `access/api`, `usecase/session`, or `usecase/channel` in the first cut unless the code actually needs them.

## Success Criteria

- no code remains under `internal/service`
- gateway-specific types are confined to `internal/access/gateway`
- message sending logic is callable without importing `internal/gateway`
- `internal/app` wires explicit collaborators instead of one umbrella service object
- adding HTTP API later requires adding a new adapter, not restructuring existing business code
