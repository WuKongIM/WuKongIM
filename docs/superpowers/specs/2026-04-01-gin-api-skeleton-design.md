# Gin API Skeleton Design

## Overview

Add a minimal HTTP API ingress under `internal/access/api` using Gin.

The purpose of this change is not to build a full public API. The purpose is to establish the first non-gateway ingress on top of the new `access -> usecase -> runtime` structure, proving that the architecture remains clean when a second entry path is introduced.

The API should stay intentionally narrow:

- one thin Gin server
- one health endpoint
- one message-send endpoint
- no auth
- no middleware framework
- no API docs
- no broad DTO layer

## Goals

- Introduce `internal/access/api` as the HTTP ingress adapter
- Use Gin as the HTTP framework
- Keep the API adapter thin and use-case driven
- Reuse `internal/usecase/message` instead of adding API-specific business logic
- Integrate the API server into `internal/app` as an optional runtime
- Keep configuration and lifecycle minimal

## Non-Goals

- Building a production-ready public REST API
- Implementing authentication or authorization
- Designing long-term resource-oriented REST semantics
- Adding admin endpoints, metrics, or Swagger
- Adding generic middleware/plugin abstractions

## Recommended Approach

Use a thin `Server` under `internal/access/api`:

- owns a `*gin.Engine`
- owns an `*http.Server`
- exposes `Start()` and `Stop(ctx)` lifecycle methods
- translates HTTP JSON payloads into `message.SendCommand`
- translates `message.SendResult` into JSON responses

This mirrors the role of `internal/access/gateway`, but for HTTP instead of frame-based traffic.

## Routes

### `GET /healthz`

Purpose:

- process liveness/readiness skeleton

Response:

```json
{"status":"ok"}
```

### `POST /api/messages/send`

Purpose:

- prove that API ingress can reuse `message.App`

Request body:

```json
{
  "sender_uid": "u1",
  "channel_id": "u2",
  "channel_type": 1,
  "payload": "aGk="
}
```

Notes:

- `payload` is base64-encoded string to keep the JSON boundary explicit for binary content
- all other send fields remain optional in the first version

Response body:

```json
{
  "message_id": 1,
  "message_seq": 1,
  "reason": 1
}
```

Error response shape:

```json
{
  "error": "message"
}
```

## Package Layout

```text
internal/access/api/
  server.go
  routes.go
  health.go
  message_send.go
  server_test.go
  integration_test.go
```

## Responsibilities

### `internal/access/api`

Responsibilities:

- HTTP routing
- JSON binding/validation
- base64 payload decoding
- mapping to `message.SendCommand`
- mapping `message.SendResult` to JSON
- HTTP lifecycle

Non-responsibilities:

- business rules
- online registry ownership
- storage or cluster construction

### `internal/usecase/message`

No new business rules are required for this first API skeleton.

The API adapter should call the existing usecase directly.

## App Integration

Add a minimal optional API runtime to `internal/app`.

Suggested config shape:

```go
type APIConfig struct {
    ListenAddr string
}
```

Integration rules:

- if `ListenAddr == ""`, API is disabled
- if configured, `internal/app` constructs the API server and starts it after cluster and before or alongside gateway
- shutdown should stop the API server before cluster/storage close

Read/write timeouts can wait for a later change. This first version should stay minimal.

## Dependency Direction

The API layer should follow the same rules as gateway:

```text
internal/access/api -> internal/usecase/message
internal/usecase/message -> runtime/pkg only
internal/app -> access/api + access/gateway + usecase/message + runtime/*
```

Hard rules:

- `internal/usecase/message` must not import Gin
- `internal/access/api` must not bypass usecases and call store/cluster directly
- `internal/app` remains the composition root

## Main Types

### `api.Server`

Suggested shape:

```go
type Server struct {
    engine   *gin.Engine
    http     *http.Server
    messages MessageUsecase
}
```

Where:

```go
type MessageUsecase interface {
    Send(cmd message.SendCommand) (message.SendResult, error)
}
```

## Validation Rules

First version validation should stay minimal:

- `sender_uid` required
- `channel_id` required
- `channel_type` required
- `payload` must be valid base64

Everything else can rely on the existing usecase semantics.

## Test Strategy

The API skeleton should be covered at two levels.

### Unit tests

- `GET /healthz` returns `200` with `{"status":"ok"}`
- `POST /api/messages/send` maps JSON to `message.SendCommand`
- invalid JSON or invalid base64 returns `400`
- usecase errors return `500`

### Integration test

- construct a real `message.App`
- construct a real API server
- `POST /api/messages/send` returns the same result shape as the usecase

The integration test does not need to prove actual recipient delivery over gateway. That is already covered elsewhere. Here the goal is ingress reuse, not cross-ingress end-to-end routing.

## Success Criteria

- repository gains a minimal Gin API ingress under `internal/access/api`
- API start/stop is integrated into `internal/app`
- `POST /api/messages/send` reuses `message.App`
- no API-specific business logic is introduced
- architecture remains additive: gateway and API are sibling adapters over shared usecases
