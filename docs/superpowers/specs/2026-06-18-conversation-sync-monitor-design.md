# Conversation Sync Monitor Design

## Summary

Extend the business realtime monitor at `/business/monitor` with conversation
health cards. The primary client path is `/conversation/sync`, not
`/conversation/list`, so the monitor should focus on legacy-compatible sync
request experience plus the active-conversation projection pipeline that makes
recent conversations visible after message commits.

The page remains one business message-path monitor. Conversation health is added
as a new path stage between append/commit and online delivery:

```text
Send Entry -> Append Commit -> Conversation Sync -> Online Delivery -> Offline Retry -> Error Closure
```

## Goals

- Show whether clients can synchronize recent conversations quickly and
  successfully through `/conversation/sync`.
- Show whether committed messages are being admitted into the conversation
  active cache and flushed to durable UID-owned metadata without visible lag.
- Reuse the existing `/manager/monitor/realtime` card response shape so the web
  monitor does not need a new API contract.
- Keep labels low-cardinality. Do not label metrics by UID, channel ID, device,
  client message number, or peer address.
- Keep `/conversation/list` as a secondary compatibility read path. It can be
  observed, but it is not the headline client SLO for this monitor.

## Non-Goals

- No per-UID or per-channel conversation drill-down in the realtime monitor.
- No alert-rule editor, notification routing, or threshold configuration.
- No new standalone conversation monitor page.
- No change to `/conversation/sync` response semantics.
- No client-visible API migration away from the legacy sync request fields.

## Current State

The web monitor already renders cards from `GET /manager/monitor/realtime`.
Each card has a stable key, stage, tone, unit, latest value, series, stats, and
availability fields. The frontend maps those generic cards to local labels and
colors.

The backend Prometheus provider already assembles business monitor cards in
`internalv2/app/manager_monitor_prometheus.go`. It currently covers send entry,
append/commit, online delivery, offline retry, path errors, and active
connections.

Conversation observability is uneven:

- `/conversation/list` already records a low-cardinality observation event.
- `/conversation/sync` is the main client path but does not yet record request
  metrics.
- Active-conversation cache, dirty-row age, flush duration, flush result, and
  authority pressure metrics already exist under `pkg/metrics/conversation.go`.

## Recommended Approach

Use one new `Conversation Sync` monitor stage inside `/business/monitor`.

This is better than a standalone page because operators usually debug recent
conversation issues from the message path: a message enters, commits, updates
the conversation projection, and then gets delivered. If conversation cards sit
between append/commit and delivery, the dashboard can show exactly where the
path begins to degrade.

This is also better than adding only a snapshot value because conversation
failures have two different shapes:

- Client sync is slow or returning errors.
- Sync is healthy, but the active projection is behind because dirty rows are
  not flushing or authority admission is under pressure.

The monitor needs both signals to avoid false diagnosis.

## Metric Groups

### Client Sync Experience

These metrics describe `/conversation/sync`, the main client-facing recent
conversation synchronization route.

1. **Conversation Sync Rate**
   - Card key: `conversationSyncRate`
   - Stage: `conversationSync`
   - Unit: `req/s`
   - Primary value: request rate across all results.
   - Prometheus source: `sum(rate(wukongim_conversation_sync_total[rateWindow]))`
   - Purpose: detect traffic drops, reconnect storms, and sync bursts.

2. **Conversation Sync Latency P99**
   - Card key: `conversationSyncLatencyP99`
   - Stage: `conversationSync`
   - Unit: `ms`
   - Primary value: P99 handler latency for successful and failed sync attempts.
   - Prometheus source:
     `histogram_quantile(0.99, sum(rate(wukongim_conversation_sync_duration_seconds_bucket[rateWindow])) by (le)) * 1000`
   - Purpose: show whether clients can refresh recent conversations quickly.

3. **Conversation Sync Error Rate**
   - Card key: `conversationSyncErrorRate`
   - Stage: `conversationSync`
   - Unit: `%`
   - Primary value: non-`ok` result percentage.
   - Prometheus source:
     `sum(rate(wukongim_conversation_sync_total{result!="ok"}[rateWindow])) / clamp_min(sum(rate(wukongim_conversation_sync_total[rateWindow])), 1) * 100`
   - Purpose: distinguish healthy latency from failed sync traffic.

4. **Conversation Returned Items**
   - Card key: `conversationReturnedItems`
   - Stage: `conversationSync`
   - Unit: `items`
   - Primary value: average returned conversations per sync.
   - Prometheus source:
     `sum(rate(wukongim_conversation_sync_returned_items_sum{result="ok"}[rateWindow])) / clamp_min(sum(rate(wukongim_conversation_sync_returned_items_count{result="ok"}[rateWindow])), 1)`
   - Purpose: catch sudden empty working sets, unexpectedly large pages, and
     only-unread behavior changes.

5. **Conversation Recent Load P99**
   - Card key: `conversationRecentLoadLatencyP99`
   - Stage: `conversationSync`
   - Unit: `ms`
   - Primary value: P99 recent-message load latency when `msg_count > 0`.
   - Prometheus source:
     `histogram_quantile(0.99, sum(rate(wukongim_conversation_sync_recent_load_duration_seconds_bucket[rateWindow])) by (le)) * 1000`
   - Purpose: locate sync latency caused by loading recent messages rather than
     active-view selection.
   - Availability: show no-data state when there are no sync requests with
     recents in the selected window.

### Projection Freshness

These metrics describe whether committed messages become recent-conversation
active rows in a timely way.

6. **Conversation Active Dirty Rows**
   - Card key: `conversationActiveDirtyRows`
   - Stage: `conversationSync`
   - Unit: rows
   - Primary value: current dirty rows in the active cache.
   - Prometheus source: `sum(wukongim_conversation_active_cache_dirty_rows)`
   - Purpose: show projection backlog waiting to flush.

7. **Conversation Oldest Dirty Age**
   - Card key: `conversationActiveOldestDirtyAge`
   - Stage: `conversationSync`
   - Unit: `s`
   - Primary value: age of the oldest dirty active row.
   - Prometheus source:
     `max(wukongim_conversation_active_cache_oldest_dirty_age_seconds)`
   - Purpose: directly express how stale recent conversation visibility may be.
   - Priority: this is the most important projection freshness card.

8. **Conversation Active Flush Latency P99**
   - Card key: `conversationActiveFlushLatencyP99`
   - Stage: `conversationSync`
   - Unit: `ms`
   - Primary value: P99 dirty-row flush latency.
   - Prometheus source:
     `histogram_quantile(0.99, sum(rate(wukongim_conversation_active_flush_duration_seconds_bucket[rateWindow])) by (le)) * 1000`
   - Purpose: show slow durable writes to UID-owned metadata.

9. **Conversation Active Flush Error Rate**
   - Card key: `conversationActiveFlushErrorRate`
   - Stage: `conversationSync`
   - Unit: `%`
   - Primary value: non-`ok` and non-`no_dirty` flush attempt percentage.
   - Prometheus source:
     `sum(rate(wukongim_conversation_active_flush_total{result!~"ok|no_dirty"}[rateWindow])) / clamp_min(sum(rate(wukongim_conversation_active_flush_total[rateWindow])), 1) * 100`
   - Purpose: catch durable flush failure without treating `no_dirty` as an
     error.

10. **Conversation Authority Pressure Rate**
    - Card key: `conversationAuthorityPressureRate`
    - Stage: `conversationSync`
    - Unit: `events/s`
    - Primary value: cache-pressure and route-failure events per second.
    - Prometheus source:
      `sum(rate(wukongim_conversation_authority_cache_pressure_total{result!="ok"}[rateWindow])) + sum(rate(wukongim_conversation_authority_admit_total{result=~"cache_pressure|route_not_ready|stale_route|not_leader|timeout"}[rateWindow]))`
    - Purpose: show whether active patch admission is blocked by authority
      routing, fencing, or cache pressure.

## New Backend Metrics

Add sync metrics to `pkg/metrics/conversation.go` and wire them from
`internalv2/access/api/conversation_sync.go` through `internalv2/app/observability.go`.

Recommended metric families:

```text
wukongim_conversation_sync_total{result,only_unread,with_recents}
wukongim_conversation_sync_duration_seconds_bucket{result,only_unread,with_recents}
wukongim_conversation_sync_returned_items_bucket{result,only_unread,with_recents}
wukongim_conversation_sync_overlay_items_bucket{result,only_unread,with_recents}
wukongim_conversation_sync_recent_load_duration_seconds_bucket{result,only_unread}
```

Allowed `result` labels:

```text
ok
invalid_request
parse_last_msg_seqs_error
not_configured
store_error
recent_message_error
error
```

Use `store_error` and `recent_message_error` only when the usecase or adapter
can identify the failing phase. The first implementation may conservatively use
`error` for internal sync failures until phase-specific errors or observations
are exposed. Do not infer `recent_message_error` only from `msg_count > 0`.

`only_unread` should be `true` or `false`. `with_recents` should be `true` when
`msg_count > 0`, otherwise `false`.

Do not add UID, channel ID, channel type, message ID, client message number, or
error text as labels.

## Sync Observation Shape

The access adapter should record one observation for every `/conversation/sync`
request, including invalid requests. A small event DTO is enough:

```text
Result
Duration
OnlyUnread
WithRecents
ReturnedItems
OverlayItems
RecentLoadDuration
```

`OverlayItems` counts client-known `last_msg_seqs` candidates that were not in
the active scan window. It is useful because high overlay pressure can make sync
cost grow even when returned items stay small.

The usecase currently owns the internal candidate selection and recent-message
load. If collecting `OverlayItems` or `RecentLoadDuration` would make the first
implementation too invasive, add `sync_total`, `sync_duration`, and
`returned_items` first, then extend the usecase result with low-cardinality shape
facts in a follow-up.

## Monitor API Mapping

Keep `GET /manager/monitor/realtime` response-compatible. Add new card
definitions to the existing Prometheus provider. The web page should ignore
unknown card keys as it does today, so backend-first and frontend-first rollout
remain safe.

Add a new realtime monitor stage constant:

```text
conversationSync
```

The frontend maps it to:

```text
monitor.stage.conversationSync = Conversation Sync / 会话同步
```

## Snapshot Strip

Add at most four conversation snapshot entries so the strip stays readable:

1. `conversationSyncP99` from `conversationSyncLatencyP99`
2. `conversationSyncErrors` from `conversationSyncErrorRate`
3. `conversationDirtyAge` from `conversationActiveOldestDirtyAge`
4. `conversationFlushErrors` from `conversationActiveFlushErrorRate`

These answer the top-level operator questions:

- Are clients syncing slowly?
- Are client syncs failing?
- Are recent conversations stale?
- Is projection flushing failing?

## Web UI Changes

Update `web/src/pages/monitor` only within the existing monitor model:

- Add `conversationSync` to `MonitorStage`.
- Add the ten conversation card keys to `MonitorMetricKey`.
- Add chart config entries with distinct, restrained colors.
- Add i18n labels in English and Chinese.
- Add no-data label for recent-load latency samples.
- Keep the existing card wall layout and realtime toolbar.

No route or navigation changes are needed because this is an extension of
`/business/monitor`.

## Error Handling

- If Prometheus has no sync metrics yet, cards should render unavailable with
  no-data labels rather than making the whole monitor fail.
- If some conversation cards are unavailable but message-path cards are present,
  the monitor status remains `partial`.
- `no_dirty` flush results are healthy and must not count as errors.
- Histogram cards with no samples should not fall back to zero latency; zero
  would falsely imply healthy behavior.

## Testing

Backend tests:

- `pkg/metrics` registry test for new sync metric families and label
  normalization.
- `internalv2/access/api` test that `/conversation/sync` records observations
  for ok, invalid request, parse error, not configured, store error, and recent
  message error.
- `internalv2/app` observability test that sync observations update Prometheus
  metrics.
- `internalv2/app` monitor provider tests that new PromQL card keys are present,
  partial no-data behavior is preserved, and `no_dirty` flushes are not errors.

Frontend tests:

- `web/src/pages/monitor/page.test.tsx` renders conversation cards returned by
  the realtime monitor API.
- `web/src/pages/monitor/preview-data.test.ts` or equivalent model test keeps
  card order as message path order.
- i18n/page shell tests continue to pass in English and Chinese.

Focused verification:

```text
go test ./pkg/metrics ./internalv2/access/api ./internalv2/app
cd web && bun run test -- src/pages/monitor/page.test.tsx src/pages/monitor/preview-data.test.ts src/pages/page-shells.test.tsx
cd web && bunx tsc -b
```

## Rollout Notes

Implement the sync request metrics before adding the cards to the default
monitor definition. Existing active-cache and authority metrics can be wired in
the same monitor-provider change because they already exist.

If this is rolled out against nodes without the new sync metrics, the monitor
will show partial conversation data from active-cache metrics and no-data states
for sync request cards until the nodes are upgraded.
