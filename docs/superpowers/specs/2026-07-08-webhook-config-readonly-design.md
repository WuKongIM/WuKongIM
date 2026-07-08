# Webhook Config Readonly Design

## Context

The manager web route `/system/webhooks` currently renders a placeholder. The
runtime webhook subsystem already exists and is driven by startup configuration
from `WK_WEBHOOK_*` keys. It is node-local, bounded, best-effort, and must not
affect SENDACK, durable append, conversation active admission, or owner
delivery.

This design turns the page into an operations view of the current startup
configuration. It deliberately does not introduce runtime mutation,
configuration persistence, callback logs, or test-send behavior.

## Goals

- Show the effective webhook startup configuration in the manager UI.
- Make disabled and enabled states clear without implying hot reload support.
- Keep the backend read-only and scoped to the current node process.
- Preserve webhook performance semantics: no new work on the send path and no
  blocking dependency on external endpoints.
- Align the example config with the existing `WK_WEBHOOK_*` parser.

## Non-Goals

- No write API for webhook settings.
- No runtime hot reload of webhook workers, queues, event filters, or endpoint.
- No cluster-wide aggregation of per-node webhook configuration.
- No callback delivery log retention page.
- No test webhook send action.

## Backend API

Add a read-only manager route:

```text
GET /manager/webhooks/config
```

The route returns the current process's normalized webhook configuration:

```json
{
  "enabled": true,
  "http_addr": "http://127.0.0.1:19090/webhook",
  "focus_events": ["msg.notify", "msg.offline"],
  "supported_events": ["msg.notify", "msg.offline", "user.onlinestatus"],
  "queue_size": 1024,
  "workers": 16,
  "msg_notify_batch_max_items": 100,
  "msg_notify_batch_max_wait": "500ms",
  "online_status_batch_max_items": 512,
  "online_status_batch_max_wait": "2s",
  "offline_uid_batch_size": 512,
  "request_timeout": "5s",
  "retry_max_attempts": 3,
  "source": "startup_config",
  "requires_restart": true
}
```

The route requires `cluster.webhook:r` when manager auth is enabled. Add
`cluster.webhook` to the manager permission catalog with read-only action `r`.

Implementation boundary:

- `internal/app` owns the effective `WebhookConfig` after normalization.
- `internal/access/manager` should not depend directly on `internal/app.Config`.
- Add a narrow provider interface to the manager server options, for example
  `WebhookConfigProvider`, that returns a DTO or a small entry-neutral snapshot.
- HTTP owns permission checks, route registration, and JSON response mapping.
- If the provider is not wired, return `503 service_unavailable`.

## Frontend Page

Replace the `/system/webhooks` placeholder with a compact read-only view:

- Status strip: enabled state, source `startup_config`, and
  `requires_restart=true`.
- Callback target section: show `http_addr`, or an empty state when not
  configured.
- Event section: list supported events and show which events are active. An
  empty `focus_events` means all supported events are active.
- Runtime parameters section: queue size, workers, notify batch limit/wait,
  online-status batch limit/wait, offline UID chunk size, request timeout, and
  retry attempts.
- Notice: changes must be made in `wukongim.conf` or `WK_WEBHOOK_*`
  environment variables and require process restart.

The page must not render save buttons, switches, destructive controls, or a
test-send button in this version.

## Config Example

Add a `WK_WEBHOOK_*` section to `wukongim.conf.example` near delivery or other
side-effect runtime settings. Comments should explain:

- webhook delivery is node-local and best-effort;
- queues are bounded to protect high-throughput send paths;
- failures and retry exhaustion do not change SENDACK or durable append success;
- list fields use one JSON string value;
- changes require restart.

## Testing

Backend:

- Manager route returns a normalized webhook configuration snapshot.
- Auth requires `cluster.webhook:r`.
- Missing provider maps to `503 service_unavailable`.
- App/provider exposes normalized defaults, including `Enabled=true` when
  `HTTPAddr` is configured.

Frontend:

- `manager-api` calls `/manager/webhooks/config`.
- Page renders enabled and disabled states.
- Empty `focus_events` is presented as all supported events.
- 403 and 503 errors use existing resource-state behavior.
- Page shell tests no longer expect the Webhook page to be "Coming Soon".

Focused verification:

```bash
GOWORK=off go test ./internal/app ./internal/access/manager -run 'Webhook|ManagerWebhook|Permissions' -count=1
cd web && /Users/tt/.bun/bin/bun run test -- src/lib/manager-api.test.ts src/pages/settings/webhooks/page.test.tsx src/pages/page-shells.test.tsx
cd web && /Users/tt/.bun/bin/bun run build
git diff --check
```

## Open Decisions

None for this read-only slice. Runtime writes, persistent storage, cluster-wide
config comparison, callback log retention, and test-send behavior should be
designed separately.

