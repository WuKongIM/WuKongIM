# User Send Rate Limit Design

## Context

WuKongIM currently has message send orchestration in `internal/usecase/message` and gateway protocol adaptation in `internal/access/gateway`. User rate limiting should protect the full send path without tying the rule to one entry protocol. A single-node deployment remains a single-node cluster, so the first implementation must not introduce a bypass path that treats single-node mode differently.

This design focuses on user send rate limiting: limiting how frequently one UID may submit messages. HTTP API rate limiting, connection-rate limiting, and delivery fanout backpressure are separate concerns and should use separate limiters if needed.

## Goals

- Enforce send rate limits for all message entry paths that call `message.App.Send`.
- Reject over-limit sends before permission reads, plugin hooks, and channel append work.
- Keep the hot path O(1), allocation-light, and free of storage writes.
- Preserve project layering: access adapters stay thin, usecases orchestrate policy, runtime packages hold reusable node-local primitives.
- Keep the implementation extensible for later cluster-wide quota leasing.

## Non-Goals

- Strict cluster-wide global quota in the first version.
- HTTP manager/API generic request throttling.
- Delivery fanout throttling or channel runtime backpressure.
- Durable per-user quota accounting.

## Recommended Approach

Implement the first version as a node-local UID send limiter under `internal/runtime/userlimit`, injected into `internal/usecase/message.App`. The limiter uses sharded in-memory token buckets with lazy refill. The message usecase calls the limiter immediately after authenticating `FromUID` and before channel type validation, permission checks, plugin hooks, realtime dispatch, or durable append.

This gives the best first-step trade-off: high performance, low complexity, and one unified business enforcement point. The public interface should be designed so a later implementation can replace the node-local limiter with an owner-based quota lease limiter without changing `message.App.Send` semantics.

## Architecture

```text
access/gateway or access/api or plugin host
  -> message.App.Send
      -> validate FromUID
      -> UserSendLimiter.AllowSend
          -> allowed: continue
          -> rejected: return SendResult{Reason: ReasonRateLimit}
      -> checkSendPermission
      -> beforeSendHook
      -> sendRealtime or sendDurable
```

### Runtime Package

Create:

```text
internal/runtime/userlimit/
  limiter.go
  bucket.go
  sharded_map.go
  metrics.go
```

Core interface exposed to the message usecase:

```go
// UserSendLimiter decides whether a user send may enter the expensive send path.
type UserSendLimiter interface {
    AllowSend(now time.Time, req UserSendLimitRequest) UserSendLimitDecision
}
```

Suggested request and decision model:

```go
// UserSendLimitRequest identifies the sender and message dimension being checked.
type UserSendLimitRequest struct {
    UID         string
    ChannelID   string
    ChannelType uint8
    Origin      message.SendOrigin
    IsSystemUID bool
}

// UserSendLimitDecision reports whether a send is admitted and when it may be retried.
type UserSendLimitDecision struct {
    Allowed    bool
    RetryAfter time.Duration
    RuleName   string
}
```

If importing `message.SendOrigin` into runtime would violate dependencies, use a small runtime-local origin enum or string and map it in `message.App`.

### Message Usecase Integration

Add to `message.Options`:

```go
// UserSendLimiter rejects over-limit user sends before expensive send-path work.
UserSendLimiter UserSendLimiter
```

Add to `message.App` and call near the top of `Send`:

```text
1. reject unauthenticated sender
2. check user send limiter
3. continue existing validation and send flow
```

Over-limit sends should return a business `SendResult` reason instead of an error, so gateway writes a normal sendack and does not map the result to `ReasonSystemError`.

## Token Bucket Behavior

Use token bucket because it handles steady rate and burst capacity with constant-time state updates.

Each bucket stores only:

```text
tokens
last_refill_unix_nano
last_seen_unix_nano
```

Implementation details:

- Use shard count such as 256 by default.
- Route UID keys by stable hash to one shard.
- Lock only the target shard.
- Refill lazily during `AllowSend`; do not run per-bucket tickers.
- Run a low-frequency janitor to evict buckets idle longer than `IdleTTL`.
- Enforce `MaxBuckets` to avoid unbounded memory growth under UID cardinality attacks.
- Avoid UID as a metrics label.

## Configuration

Add fields under `app.MessageConfig` with detailed English comments:

```text
UserRateLimitEnabled
UserRateLimitRate
UserRateLimitBurst
UserRateLimitBucketShards
UserRateLimitIdleTTL
UserRateLimitMaxBuckets
UserRateLimitSystemUIDBypass
UserRateLimitPluginBypass
```

Suggested env keys:

```text
WK_MESSAGE_USER_RATE_LIMIT_ENABLED=false
WK_MESSAGE_USER_RATE_LIMIT_RATE=100/s
WK_MESSAGE_USER_RATE_LIMIT_BURST=200
WK_MESSAGE_USER_RATE_LIMIT_BUCKET_SHARDS=256
WK_MESSAGE_USER_RATE_LIMIT_IDLE_TTL=10m
WK_MESSAGE_USER_RATE_LIMIT_MAX_BUCKETS=100000
WK_MESSAGE_USER_RATE_LIMIT_SYSTEM_UID_BYPASS=true
WK_MESSAGE_USER_RATE_LIMIT_PLUGIN_BYPASS=false
```

When adding these keys, update `wukongim.conf.example` in the same change.

## Protocol Reason

If `pkg/protocol/frame` already has a suitable reason such as `ReasonRateLimit` or `ReasonTooManyRequests`, reuse it. Otherwise add one explicit reason, preferably:

```go
ReasonRateLimit
```

`message.App.Send` should return:

```go
SendResult{Reason: frame.ReasonRateLimit}
```

The gateway should not need special error mapping when the usecase returns a normal `SendResult`.

## Bypass Rules

Default behavior:

- System UIDs bypass user rate limiting when `UserRateLimitSystemUIDBypass` is true.
- Plugin-origin sends do not bypass by default.
- `SkipPluginHooks` must not imply rate-limit bypass.
- If trusted internal paths need bypass later, add an explicit `BypassUserRateLimit` field and ensure only internal code can set it.

## Observability

Add low-cardinality metrics:

```text
message_user_rate_limit_allowed_total
message_user_rate_limit_rejected_total
message_user_rate_limit_active_buckets
message_user_rate_limit_bucket_evicted_total
message_user_rate_limit_decision_duration_seconds
```

Sample logs for over-limit sends:

```text
event=message.user_rate_limit.rejected uid=<uid> rule=<rule> retry_after_ms=<ms>
```

Do not log every rejection without sampling, because a hot UID can otherwise amplify log volume.

## Future Cluster-Wide Extension

The first version is node-local. For strict or near-strict multi-node semantics, keep the `UserSendLimiter` interface and add an owner-backed quota lease implementation later.

Future flow:

```text
message.App.Send
  -> quota lease limiter
      -> local lease has tokens: deduct locally
      -> lease exhausted: request more quota from UID owner
      -> owner grants bounded token lease with TTL
```

This avoids one RPC per message while keeping total cluster usage close to the configured quota. Lease size and TTL control the trade-off between accuracy and latency.

## Testing Strategy

Unit tests should stay fast.

Runtime tests:

- Allows burst up to configured capacity.
- Refills according to elapsed time.
- Rejects when tokens are exhausted.
- Computes retry-after.
- Evicts idle buckets.
- Enforces max bucket limit.
- Handles concurrent `AllowSend` safely.

Message usecase tests:

- Over-limit send returns `ReasonRateLimit`.
- Over-limit send does not call permission store.
- Over-limit send does not call plugin hook.
- Over-limit send does not append to channel log.
- System UID bypass works when enabled.
- Plugin-origin send is limited by default.

Gateway tests:

- A rate-limited send writes sendack with `ReasonRateLimit`.

Config tests:

- New `WK_MESSAGE_USER_RATE_LIMIT_*` keys parse correctly.
- Defaults preserve legacy behavior with rate limiting disabled.
- `wukongim.conf.example` includes the new keys.

Suggested targeted test command:

```bash
go test ./internal/runtime/... ./internal/usecase/message ./internal/access/gateway ./cmd/wukongim
```

## Implementation Order

1. Add protocol reason if missing.
2. Add `internal/runtime/userlimit` token bucket implementation and tests.
3. Add message usecase interface and early send-path check.
4. Wire limiter construction in `internal/app/build.go` from `MessageConfig`.
5. Add config parsing, validation, defaults, and `wukongim.conf.example` entries.
6. Add gateway/usecase/config tests.
7. Run targeted tests.
