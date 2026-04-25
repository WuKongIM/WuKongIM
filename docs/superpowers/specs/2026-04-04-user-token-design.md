# User Token API Design

## Overview

Implement `POST /user/token` in the new Go monorepo and keep its external behavior aligned with the old version in `learn_project/WuKongIM/internal/api/user.go`.

The old endpoint is not only an HTTP adapter. It also depends on a storage model where user existence and per-device token state are stored separately. The current repo does not have that separation yet. `metadb.User` currently stores `token`, `device_flag`, and `device_level` on a single `uid` record, which cannot preserve old semantics when the same user logs in from multiple device flags.

To make `/user/token` truly compatible, this design adds an explicit device metadata path and implements the endpoint through the current layered architecture:

- `internal/access/api` for HTTP adaptation
- `internal/usecase/user` for business orchestration
- `pkg/storage/metastore` for distributed metadata facade
- `pkg/storage/metafsm` and `pkg/storage/metadb` for replicated storage

## Goals

- Add `POST /user/token`
- Preserve the old request path, request fields, and response shape
- Preserve old per-device token semantics for `uid + device_flag`
- Preserve old master-device kick behavior for local online sessions
- Keep the implementation aligned with current layering rules
- Avoid reintroducing a generic `service` layer

## Non-Goals

- Implement other old `/user/*` endpoints in the same task
- Add old explicit HTTP leader-forwarding behavior at the access layer
- Change current gateway token verifier wiring in this task
- Add old system-account rejection in this task
- Add old `HasPermissionForChannel(uid, person)` permission checks in this task
- Add a migration tool for pre-existing single-record user token data

## Problem Statement

The old implementation assumes two separate concepts:

1. a user record that guarantees the user exists
2. a device record keyed by `uid + device_flag` that stores token and device level

The new repo currently only has:

- `metadb.User{UID, Token, DeviceFlag, DeviceLevel}`
- `metastore.UpsertUser/GetUser`
- message and gateway code that only needs `GetUser(uid)`

This creates a semantic mismatch:

- writing token data for `uid=u1, device_flag=APP`
- then writing token data for `uid=u1, device_flag=WEB`

would overwrite the same user record instead of preserving two device states.

That is incompatible with the old `/user/token` behavior and would make future gateway token verification weaker as well.

## Current Constraints

### Cluster Semantics

This repo treats every deployment as cluster-based, including single-node deployments. The new endpoint must not add a special non-cluster write path.

The current `raftcluster.Cluster.Propose` path already forwards follower proposals to the current leader. Because of that, the new API does not need to reproduce the old explicit HTTP-level leader lookup and forwarding logic.

### Existing User Reads

Current code already depends on `GetUser(uid)` for identity existence checks. That API must remain stable so existing message flows are not disturbed by the `/user/token` work.

### Online Session Runtime

Local online connections are available through `internal/runtime/online.Registry`. Each connection already carries:

- `UID`
- `DeviceFlag`
- `DeviceLevel`
- `Session`

That is sufficient to implement the old "master device update kicks old local connections on the same device flag" behavior.

## Recommended Design

### 1. Keep `User` for existence, add `Device` for per-device auth state

Do not overload `metadb.User` further.

Keep:

- `metadb.User`
- `metastore.GetUser`
- `metastore.UpsertUser`

Add:

- `metadb.Device`
- `metastore.GetDevice`
- `metastore.UpsertDevice`

This keeps existing `GetUser(uid)` consumers stable while adding a correct place for per-device token state.

### 2. Add a replicated `Device` metadata path

Introduce a new device table and command path:

- `pkg/storage/metadb`: `Device` table, codec helpers, read/write APIs, batch support
- `pkg/storage/metafsm`: `UpsertDevice` command encode/decode/apply
- `pkg/storage/metastore`: cluster-routed `GetDevice` and `UpsertDevice`

Device writes use the same slot routing rule as user writes: route by `uid`.

### 3. Implement the business logic in `internal/usecase/user`

Create a dedicated user use case package instead of placing business logic in `access/api`.

The use case owns:

- request validation
- user existence guarantee
- device upsert
- local master-device kick behavior

The API layer only translates HTTP JSON into a use case command and maps use case results/errors back into the old response envelope.

### 4. Preserve old external API shape

The endpoint must keep the old public contract:

- path: `POST /user/token`
- request JSON fields:
  - `uid`
  - `token`
  - `device_flag`
  - `device_level`
- success response: HTTP `200`, body `{"status":200}`
- validation or business/storage failure: HTTP `400`, body `{"msg":"<error>","status":400}`

This task intentionally does not reuse the newer `/message/send` error style.

## Detailed Design

### HTTP Access Layer

Add a new handler in `internal/access/api` and register:

```text
POST /user/token
```

Responsibilities:

- bind the old request shape
- call the user use case
- write old response JSON

It must not:

- read/write storage directly
- examine cluster leadership directly
- manipulate sessions directly

### Request Contract

Request body:

```json
{
  "uid": "u1",
  "token": "token-1",
  "device_flag": 0,
  "device_level": 1
}
```

Validation rules for this task:

- `uid` must be non-empty
- `token` must be non-empty
- `uid` must not contain any of the legacy forbidden characters: `@`, `#`, `&`

Validation rules explicitly deferred:

- reject system uid
- permission check through `HasPermissionForChannel(uid, person)`

### Response Contract

Success:

```json
{
  "status": 200
}
```

Failure:

```json
{
  "msg": "uid不能为空！",
  "status": 400
}
```

HTTP status remains:

- `200` on success
- `400` on request validation or business/storage error

### Use Case Package

Add `internal/usecase/user`.

Suggested shape:

```text
internal/usecase/user/
  app.go
  command.go
  deps.go
  token.go
```

Suggested command:

```go
type UpdateTokenCommand struct {
    UID         string
    Token       string
    DeviceFlag  wkframe.DeviceFlag
    DeviceLevel wkframe.DeviceLevel
}
```

Dependencies:

- user store facade:
  - `GetUser(ctx, uid)`
  - `CreateUser(ctx, user)`
- device store facade:
  - `GetDevice(ctx, uid, deviceFlag)`
  - `UpsertDevice(ctx, device)`
- online registry:
  - `ConnectionsByUID(uid)`
- clock:
  - optional, only if needed for time-based behavior

### Use Case Flow

`UpdateToken(ctx, cmd)`:

1. validate command
2. read user by `uid`
3. if user does not exist, create a minimal user-existence record using create-only semantics
4. upsert device record keyed by `uid + device_flag` with:
   - `uid`
   - `device_flag`
   - `token`
   - `device_level`
5. if `device_level != master`, stop here
6. if `device_level == master`, load local online connections for `uid`
7. filter only connections with the same `device_flag`
8. for each matching connection:
   - write `DisconnectPacket{ReasonCode: ReasonConnectKick, Reason: "账号在其他设备上登录"}`
   - schedule `Session.Close()` after `10s`
9. return success

This matches the old local kick behavior without needing the old timing-wheel dependency.

### Kicking Old Sessions

The old version delayed the hard close by 10 seconds after sending a kick packet. Preserve that timing.

Implementation choice:

- use `time.AfterFunc(10 * time.Second, sess.Close)`

Do not add a general scheduler abstraction just for this endpoint. That would be unnecessary complexity for the current scope.

This task only kicks local sessions visible in the current process `online.Registry`. That matches current runtime boundaries and is enough for the single-node-cluster and local-node behavior already present in the repo.

## Storage Design

### New Device Model

Add a new `metadb.Device`:

```go
type Device struct {
    UID         string
    DeviceFlag  int64
    Token       string
    DeviceLevel int64
}
```

Primary key:

- `(uid, device_flag)`

Required operations:

- `CreateDevice` if needed by tests or API symmetry
- `GetDevice(ctx, uid, deviceFlag)`
- `UpdateDevice` if needed by tests or API symmetry
- `UpsertDevice(ctx, device)`
- optional `DeleteDevice` only if useful for tests

For this task, `GetDevice` and `UpsertDevice` are the minimum required production APIs.

### Key Encoding

Add device key/value encoding parallel to the existing patterns:

- new table id and column ids in `catalog.go`
- primary key encoding using `slot + table + uid + device_flag + familyID`
- family value encoding for `token` and `device_level`

Keep device state in the same slot as the owning `uid`.

### FSM Command

Add a new command:

- `cmdTypeUpsertDevice`

Add TLV fields:

- `uid`
- `device_flag`
- `token`
- `device_level`

`apply()` writes through `WriteBatch.UpsertDevice(slot, device)`.

### Metastore Facade

Add:

```go
func (s *Store) UpsertDevice(ctx context.Context, d metadb.Device) error
func (s *Store) GetDevice(ctx context.Context, uid string, deviceFlag int64) (metadb.Device, error)
```

Slot routing rule:

- `groupID := s.cluster.SlotForKey(uid)`

That keeps device data colocated with the owning user shard.

## Special Character Validation

The old code uses `options.IsSpecialChar(uid)` from the legacy codebase. The new repo does not currently have that helper in active packages.

The legacy rule is exact:

```go
func isSpecialUID(s string) bool {
    return strings.Contains(s, "@") || strings.Contains(s, "#") || strings.Contains(s, "&")
}
```

For this task, port that rule exactly for `uid` validation.

The helper should be local to this feature unless another active package already needs the same rule. Do not revive the legacy global options package just for this validation.

Accepted examples under this rule:

- `u1`
- `alice.bob`
- `user-01`

Rejected examples under this rule:

- `u@1`
- `u#1`
- `u&1`

## Error Mapping

The user endpoint should not reuse `internal/access/api/error_map.go`, because that map exists for message-send semantics and returns a different envelope.

Instead, the handler should write:

- `{"status":200}` on success
- `{"msg":"...", "status":400}` on failure

This endpoint has a deliberately old-style compatibility surface.

## Testing Strategy

Use test-first development from outside in.

### 1. Access Layer Tests

Add tests in `internal/access/api` to lock:

- route registration for `POST /user/token`
- request field names
- success response envelope
- invalid JSON response envelope
- validation failure response envelope

### 2. User Use Case Tests

Add tests in `internal/usecase/user` to lock:

- missing user causes user creation/upsert
- existing user does not block device update
- device upsert uses `uid + device_flag`
- master device update kicks local same-device sessions
- slave device update does not kick sessions
- only same `device_flag` sessions are kicked

Test doubles should verify both `DisconnectPacket` content and delayed close scheduling behavior as far as practical.

### 3. Storage Tests

Add `pkg/storage/metadb` tests for:

- two records with same `uid` and different `device_flag` can coexist
- `GetDevice(uid, flag)` returns the expected record
- `UpsertDevice` updates only the targeted device

### 4. FSM / Metastore Tests

Add tests for:

- `EncodeUpsertDeviceCommand` round trip
- state machine apply persists device data
- `metastore.UpsertDevice/GetDevice` work through the cluster-backed path

## Compatibility and Rollout Notes

- `GetUser(uid)` remains unchanged for existing consumers
- `/user/token` will be the first production user of the new device metadata path
- future gateway token verification should read from `GetDevice(uid, deviceFlag)`
- no compatibility is promised for any stale single-record token data that may already exist in a dev data directory
- the "ensure user exists" path is exact:
  - first call `GetUser(ctx, uid)`
  - only when it returns `metadb.ErrNotFound`, call `CreateUser(ctx, metadb.User{UID: uid})`
  - if `CreateUser` returns `metadb.ErrAlreadyExists`, treat that as a benign concurrent create and continue
  - if the user already exists, do not write `User` again
  - this avoids zeroing or overwriting existing `User` fields while the schema still contains `token`, `device_flag`, and `device_level`

## Open Decisions Already Resolved For This Task

- Old explicit HTTP leader forwarding: not reproduced, because replicated store writes already forward through `raftcluster`
- System uid rejection: deferred
- Permission check before update: deferred
- Session close delay: keep old `10s` behavior

## Implementation Outline

1. Add failing API tests for `/user/token`
2. Add failing use case tests for update-token orchestration
3. Add failing storage tests for per-device metadata
4. Add device model, batch support, codec helpers, and storage APIs
5. Add FSM command and metastore facade
6. Add user use case
7. Add API handler and route wiring
8. Run targeted tests
9. Run broader related test suites

## Risks

- Device-table key encoding mistakes could silently overwrite records if the composite primary key is encoded incorrectly
- Delayed-close tests can become flaky if implemented with real time sleeps instead of controllable hooks
- Reusing old response shape inside the new API package can drift if the handler accidentally shares newer helper functions

## Risk Mitigations

- Add coexistence tests for same `uid`, different `device_flag`
- Keep the delayed-close mechanism injectable in the use case tests if real timers become unstable
- Keep the `/user/token` response writer local to the handler instead of routing through message-send helpers
