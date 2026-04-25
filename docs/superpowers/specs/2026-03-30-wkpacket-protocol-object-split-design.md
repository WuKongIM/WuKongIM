# Split Protocol Objects Into `pkg/wkpacket`

**Date:** 2026-03-30

## Goal

Extract shared protocol objects out of `pkg/wkproto` into a new package, `pkg/wkpacket`, so that:

- `pkg/wkproto` is responsible only for WuKong binary protocol encoding/decoding
- `pkg/jsonrpc` is responsible only for JSON-RPC encoding/decoding and object mapping
- shared protocol objects live in one dependency-free object package

## Current Problem

`pkg/wkproto` currently mixes two responsibilities:

1. shared protocol object definitions such as `Frame`, `Framer`, enums, flags, and packet structs
2. WuKong binary protocol encoding and decoding

`pkg/jsonrpc` depends on those shared objects directly, which makes `pkg/wkproto` an accidental shared model package instead of a pure codec package.

This coupling makes the package boundaries misleading and forces unrelated protocols to depend on the binary codec package just to reuse packet types.

## Target Package Boundaries

### `pkg/wkpacket`

Owns protocol object definitions only.

Contents:

- `Frame`
- `Framer`
- `FrameType`
- `LatestVersion`
- `ReasonCode`
- `Setting`
- `DeviceFlag`
- `DeviceLevel`
- `Action`
- `StreamFlag`
- `Channel`
- packet size/channel constants currently defined alongside object types
- all protocol packet structs:
  - `ConnectPacket`
  - `ConnackPacket`
  - `SendPacket`
  - `SendackPacket`
  - `RecvPacket`
  - `RecvackPacket`
  - `DisconnectPacket`
  - `SubPacket`
  - `SubackPacket`
  - `EventPacket`
  - `PingPacket`
  - `PongPacket`

Allowed behavior in this package:

- field definitions
- enum/string helpers
- lightweight object methods such as `GetFrameType()`, `String()`, `Reset()`, `UniqueKey()`, `VerityString()`

Disallowed behavior in this package:

- binary encoding and decoding
- binary frame size calculation that depends on codec-side helpers
- JSON encoding and decoding
- dependencies on `pkg/wkproto` or `pkg/jsonrpc`

### `pkg/wkproto`

Owns WuKong binary protocol codec behavior only.

Contents:

- `Protocol`
- `WKProto`
- `DecodeFrame`
- `EncodeFrame`
- `WriteFrame`
- encoder/decoder helpers
- packet-specific binary codec functions such as:
  - `decodeConnect`
  - `encodeConnect`
  - `encodeConnectSize`
  - and corresponding functions for all packet types

`pkg/wkproto` will depend on `pkg/wkpacket` for frame interfaces, enums, and packet structs.

Protocol-specific constants that are shared across protocol adapters but are not codec behavior, such as `LatestVersion`, move to `pkg/wkpacket`.

### `pkg/jsonrpc`

Owns JSON-RPC message structures, JSON codec logic, and JSON-RPC to protocol-object mapping.

Contents remain:

- JSON-RPC request/response/notification structs
- JSON encode/decode helpers
- `ToFrame` / `FromFrame`
- `ToProto` / `FromProto` style conversion helpers

Change:

- all shared protocol object references switch from `pkg/wkproto` to `pkg/wkpacket`

## Dependency Direction

Final dependency direction must be:

- `pkg/wkpacket` -> no dependency on `pkg/wkproto` or `pkg/jsonrpc`
- `pkg/wkproto` -> `pkg/wkpacket`
- `pkg/jsonrpc` -> `pkg/wkpacket`

No reverse dependency from `pkg/wkpacket` is allowed.

## File-Level Migration

## Move to `pkg/wkpacket`

- object definitions from `pkg/wkproto/common.go`
- `pkg/wkproto/setting.go`
- `pkg/wkproto/connect.go`
- `pkg/wkproto/connack.go`
- `pkg/wkproto/send.go`
- `pkg/wkproto/sendack.go`
- `pkg/wkproto/recv.go`
- `pkg/wkproto/recvack.go`
- `pkg/wkproto/disconnect.go`
- `pkg/wkproto/sub.go`
- `pkg/wkproto/suback.go`
- `pkg/wkproto/event.go`
- `pkg/wkproto/ping.go`
- `pkg/wkproto/pong.go`

## Keep in `pkg/wkproto`

- `pkg/wkproto/protocol.go`
- `pkg/wkproto/encoder.go`
- `pkg/wkproto/decoder.go`
- all encode/decode/size functions for WuKong binary protocol

## Required Refactoring During Move

Each moved packet file must be split into:

1. object definition kept in `pkg/wkpacket`
2. binary codec functions kept in `pkg/wkproto`

For example:

- `ConnectPacket` struct and `GetFrameType()` move to `pkg/wkpacket`
- `decodeConnect`, `encodeConnect`, `encodeConnectSize` remain in `pkg/wkproto`

This same rule applies to all packet types.

Methods that currently depend on codec-side size helpers, specifically `RecvPacket.Size()`, `RecvPacket.SizeWithProtoVersion()`, and `EventPacket.Size()`, must not remain on `pkg/wkpacket` objects in their current form.

Decision:

- remove these methods from `pkg/wkpacket`
- keep binary size calculation as `pkg/wkproto` codec-internal logic
- if callers still need size calculation later, add explicit helper functions in `pkg/wkproto` as a separate codec-facing API instead of creating a reverse dependency from `wkpacket`

## Compatibility Strategy

Do not keep object aliases re-exported from `pkg/wkproto`.

Reasoning:

- the goal is to make the package boundary explicit
- re-exporting shared objects from `pkg/wkproto` would preserve the current ambiguity
- repository-wide imports can be updated directly in one change

If external compatibility becomes necessary later, it should be handled as a separate task with explicit compatibility requirements.

## Implementation Constraints

- protocol field names, field types, and semantic meaning must not change
- binary encoding order must not change
- version-gated behavior must not change
- JSON-RPC schema and wire shape must not change
- `pkg/jsonrpc` must use `wkpacket.LatestVersion` instead of `wkproto.LatestVersion`
- this refactor is structural only

## Risks

### Mixed object/codec files

Most packet files currently contain both object definitions and codec logic. This must be split carefully so that object methods migrate but codec functions stay.

### Hidden import fanout

`pkg/jsonrpc` is a known dependency, but there may be other package imports of `wkproto` object types. The repository import graph must be updated consistently.

### Dirty worktree

The current worktree already contains local modifications, including `pkg/jsonrpc` files. Implementation must inspect those files before editing and preserve unrelated changes.

## Test Strategy

The refactor is complete only if behavior is unchanged and existing tests still pass.

Required verification:

1. `pkg/wkproto` tests pass
2. `pkg/jsonrpc` tests pass
3. repository build succeeds for packages affected by import rewrites
4. add or update focused tests if needed to cover `jsonrpc.ToFrame` / `jsonrpc.FromFrame` using `wkpacket`

Recommended smoke path:

- JSON-RPC request
- convert to `wkpacket.Frame`
- encode/decode through `wkproto`
- convert back from `wkpacket.Frame`

## Implementation Outline

1. Create `pkg/wkpacket`
2. Move shared enums, interfaces, constants, and packet structs into `pkg/wkpacket`
3. Update `pkg/wkproto` to import and use `pkg/wkpacket`
4. Move packet-specific binary codec functions into `pkg/wkproto` source files if they were co-located with object definitions
5. Update `pkg/jsonrpc` conversions to import `pkg/wkpacket`
6. Rewrite remaining repository imports from shared `wkproto` object types to `wkpacket`
7. Run formatting and targeted tests

## Success Criteria

The refactor is successful when:

- `pkg/wkpacket` contains all shared protocol objects
- `pkg/wkproto` contains no shared object definitions beyond codec-only helpers
- `pkg/jsonrpc` depends on `pkg/wkpacket`, not `pkg/wkproto`, for shared frame/packet types
- existing protocol behavior is unchanged
- targeted tests pass
