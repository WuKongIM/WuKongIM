# WuKongIM v3 Documentation — Phase 11 Specification

## Goal

Complete the bilingual scenario-tutorial menu with Message Push and AI & IoT
Communication. The tutorials must be runnable from the current trusted product
HTTP surface while separating WuKongIM delivery and projection semantics from
application-owned provider integration, AI execution, device policy, and
business acknowledgements.

## Published routes

- Tutorials / Message Push
- Tutorials / AI & IoT Communication

Both routes have matching Chinese and English MDX and are included in search,
sitemap, LLM outputs, and per-page Markdown. This phase leaves no planned child
under the Tutorials group.

## Message Push boundaries

- WuKongIM does not call APNs, FCM, or another mobile-push provider. The product
  service owns provider credentials, device-token lifecycle, notification
  policy, collapse and quiet-hour rules, provider retries, and delivery
  receipts.
- `msg.offline` is emitted only for an ordinary durable message after presence
  resolution finds a recipient UID with no online route. `SyncOnce`,
  request-scoped subscriber sends, and transient `NoPersist` work do not
  produce this offline-recipient effect.
- `msg.offline` identifies UID candidates, not every offline device. A UID with
  at least one online route is not an offline candidate, so per-device policy
  requires application-owned device state and may also consume `msg.notify`.
  Collection runs before sender-echo suppression, so a person-Channel candidate
  list can include `from_uid`; the product filters sender and service identities
  according to its notification policy.
- Webhook delivery is node-local, bounded, in-memory, retry-limited,
  best-effort, and not crash-replayed. Queue pressure or retry exhaustion may
  drop an event without changing durable SEND success. The current sender adds
  no signature or shared-secret header.
- Receivers must support both plain `to_uids` and gzip/Base64
  `compress_to_uids`. Provider work uses an application outbox and an
  idempotency key such as `event + message_id + uid`.
- A notification or system notice is an application payload and policy, not a
  privileged message class. Trusted system-UID bypasses are optional and must
  not replace product authorization.

## AI streaming boundaries

- The product service owns model invocation, prompt policy, tool execution,
  cancellation, quotas, and safety. Product HTTP examples stay behind a trusted
  boundary without general product authentication.
- A stream starts from one durable base message with the legacy stream setting
  bit (`setting=2`) and a stable `client_msg_no`. Event updates use the same
  channel identity and `client_msg_no`, plus stable unique `event_id` values.
- `/message/event` currently does not perform a routed base-message existence
  check. The product service must wait for base SEND success and preserve the
  exact anchor; `message_id` in an event request is response context, not that
  missing validation.
- `stream.open`, `stream.delta`, and `stream.snapshot` may remain only in the
  bounded Slot-Leader cache and return `msg_event_seq=0`. A terminal
  `stream.finish` flushes open lanes and the finish marker in one Slot proposal.
  Leadership loss without cached lanes fails finish closed; callers replay the
  complete stream state before retrying.
- Event IDs are idempotency keys. Reusing an applied ID returns the original
  result rather than applying a different payload.
- Current public sync exposes compact event summaries through
  `/channel/messagesync` with `event_summary_mode=full`; fine-grained
  `/message/eventsync` is not registered, and `/message/event` does not itself
  push every delta to connected client Sessions. Live token rendering therefore
  needs an application-owned stream or an explicitly designed message path.

## IoT boundaries

- Every device has a stable product identity and credential policy. The default
  Beta Gateway does not automatically validate stored `/user/token` metadata.
- Durable telemetry uses ordinary Channel messages with stable
  `client_msg_no`. A group telemetry example must first provision the Channel
  and sender membership; high-rate samples are aggregated or sampled before
  they create unbounded message, webhook, conversation, or storage load.
- Recoverable server commands use `sync_once=1` on a stable source Channel.
  Each recipient binds that source through `/message/cmd/bind` before the first
  command; binding starts at the current CMD-log tail, so it does not backfill
  older commands. The separate CMD log and UID-owned discovery directory stay
  out of ordinary conversations and recover through `/message/sync` followed
  by `/message/syncack`.
- Request-scoped `subscribers` provide bounded immediate targeting but do not
  create CMD discovery membership. They must not be presented as reconnect
  recovery unless a compatible binding already exists.
- Online-only commands require both `no_persist=1` and `sync_once=1`. Plain
  non-command `NoPersist` is terminal success without realtime delivery.
- SENDACK, online write, RECVACK, and `/message/syncack` are transport or cursor
  signals, not proof that a device executed an operation. Command payloads carry
  a product idempotency key, deadline, and desired state; the device reports a
  separate durable business result.

## Validation

- Navigation tests publish the final two tutorial children and freeze the
  complete Tutorials order.
- Static-output validation includes both routes in sitemap, search, LLM, and
  per-page Markdown outputs.
- Local validation runs `bun run verify`, focused Go tests for webhook,
  message-event, CMD-sync, message, channelappend, and compatible API behavior,
  plus the repository unit suite.
- Browser QA covers both tutorials in both locales at desktop and mobile widths,
  including console output and horizontal overflow.

## Excluded

- Runtime, protocol, SDK, webhook-security, mobile-provider, model-provider, or
  device-firmware changes.
- Provider-specific APNs/FCM payload references, a public fine-grained message
  event sync API, benchmark promises, deployment, DNS, or production cutover.
