# WuKongIM Web Node Config Menu Design

Date: 2026-07-08
Status: Approved for design spec
Scope: web cluster navigation, node config page, node detail deep link

## Goal

Add a standalone `配置` menu under `集群运维` for read-only inspection of
each node's effective startup configuration.

The first slice is a single-node config browser: operators choose one node,
search and filter that node's effective config, and copy the current filtered
result. It should reuse the existing node config API and stay out of runtime
mutation, config diffing, and cluster-wide fan-out.

## Context

The backend and web node detail sheet already expose per-node effective config
snapshots through:

```text
GET /manager/nodes/:node_id/config
```

That is useful from the node detail sheet, but it is still a secondary panel.
Config inspection deserves its own cluster-operations page because operators
usually arrive with a specific question:

- Is this node using the expected cluster, gateway, message, delivery, webhook,
  plugin, log, or observability setting?
- Which node is selected and when was the snapshot generated?
- Can I quickly search for a `WK_*` key or copy the filtered values for an
  incident note?

The page must treat a single-node deployment as a single-node cluster and must
not introduce any local-only bypass path.

## Non-Goals

- No config mutation, hot reload, save button, or server-side write API.
- No node-to-node diff, multi-node matrix, or cluster fan-out in this slice.
- No new backend endpoint beyond the existing node-scoped config read.
- No raw secret display; sensitive values remain redacted exactly as returned
  by the manager API.
- No polling of config snapshots from monitor, node list, or sidebar status.
- No new charting, table, state-management, or UI dependency.

## Navigation

Add a navigation item under `集群运维`, immediately before `诊断`:

```text
实时监控
节点
槽位
频道
插件
任务
Workqueue
拓扑
配置
诊断
```

Route:

```text
/cluster/node-config
```

Legacy redirects are not required for the first slice because this is a new
route. If a future hidden or experimental path exists, redirect it to this
canonical route instead of adding another menu item.

Navigation metadata:

- Menu title: `配置` / `Config`
- Page title: `节点配置` / `Node Config`
- Path label: `CLUSTER / CONFIG`
- Description: read-only effective startup configuration by node.
- Icon: use a Lucide configuration/slider style icon such as
  `SlidersHorizontal`; avoid reusing the exact `Settings` icon from system
  permissions unless the local icon set makes that unavoidable.

## Page Layout

Use the existing editorial console style: white canvas, thin rules, compact
controls, restrained status color, dense tables, and no marketing-style copy.

### Header

Use `PageContainer` and `PageHeader`.

Header content:

- Eyebrow comes from route metadata: `CLUSTER / CONFIG`.
- Title: `节点配置`.
- Description: read-only inspection of effective startup configuration for the
  selected node.
- Actions:
  - Refresh button with icon.
  - Copy filtered result button.

The header must not contain mutation controls, test-config controls, or runtime
reload language.

### Primary Grid

Desktop layout:

```text
300px node rail | flexible config workbench
```

Mobile and narrow layouts stack vertically:

```text
node rail
config summary
config workbench
```

The node rail should not be a nested card inside another card. It is a single
`SectionCard`-style surface with its own search/filter and node rows.

## Node Rail

Purpose: make node selection explicit and avoid hiding it in a small select.

Data source:

```ts
getNodes()
```

Default selected node:

1. Local node when present.
2. First node from the response.
3. No selection state when the response is empty.

URL state:

```text
/cluster/node-config?node_id=2
```

If `node_id` is present and exists in the node list, select it. If it is absent
or stale, fall back to the default selected node and replace the URL state.

Node row content:

- Node name or `Node {node_id}`.
- Local marker when `is_local` is true.
- Health/status badge using the same node status helper as the nodes page.
- Membership role, join state, controller voter/role when available.
- Address.
- Compact slot summary.

Node rail controls:

- Search by node ID, node name, address, status, or role.
- Display filtered and total node count.
- Keyboard and screen-reader behavior should remain button/list based; do not
  make row selection depend on tiny icons.

## Config Summary

Display a compact summary strip above the config table for the selected node:

- Current node.
- Source.
- Restart requirement.
- Generated time.

These values come from `ManagerNodeConfigResponse` and selected node metadata.
The strip should use stable dimensions so status text does not shift the table
when data reloads.

## Config Workbench

Data source:

```ts
getNodeConfig(nodeId)
```

Request behavior:

- Load config only for the selected node.
- Abort or ignore stale responses when the selected node changes quickly.
- Refresh reloads both node list and the selected node config, preserving the
  selected node when still present.
- Do not load all node configs in the background.

Controls:

- Search input filters by `key`, `label`, and `value`.
- Group tabs filter by config group.
- Copy filtered result copies only currently visible rows.

Recommended group tabs:

```text
全部
节点
集群
网关
消息
投递
Webhook
插件
日志
观测
```

The implementation should derive actual available tabs from response groups,
with `全部` first. Do not hard fail when the backend adds a new group.

Table columns:

```text
键 | 标签 | 值 | 标记
```

Optional note/description text can be added later only if the API provides it.
The first slice should not invent explanatory text per key in the frontend.

Table behavior:

- Keep grouped sections so operators can scan by domain.
- Keep a sticky table header inside the scrollable result area.
- Use mono typography for keys and values.
- Wrap or truncate very long values without overflowing the page.
- Provide per-row copy for the value or full row when it can be done without
  making the row visually noisy.
- Empty filtered state is distinct from empty config response.

Flags:

- Sensitive values show returned redacted value.
- `sensitive` and `redacted` render as quiet badges.
- No reveal interaction is provided.

Copy format:

Copy the filtered rows as bounded JSON, for example:

```json
{
  "node_id": 1,
  "generated_at": "2026-07-08T10:00:00Z",
  "groups": [
    {
      "id": "cluster",
      "title": "Cluster",
      "items": [
        {
          "key": "WK_CLUSTER_HASH_SLOT_COUNT",
          "label": "Hash slot count",
          "value": "256",
          "sensitive": false,
          "redacted": false
        }
      ]
    }
  ]
}
```

Copy success should be a small inline state or toast-like status if the app
already has one. It must not open a modal.

## Node Detail Sheet

The node detail sheet should stop being the primary place for full config
inspection.

Recommended behavior:

- Keep a compact effective-config preview in the detail sheet:
  source, generated time, restart requirement, and a small count of groups/items.
- Add a clear `查看完整配置` link to:

```text
/cluster/node-config?node_id={node_id}
```

- If preserving the existing full table is cheaper for the first implementation,
  it may remain temporarily, but the menu page should be treated as canonical
  and tests should cover the deep link.

## States And Errors

Node list states:

- Loading.
- Empty.
- Forbidden.
- Unavailable.
- Generic error with retry.

Config states:

- No node selected.
- Loading selected node config.
- Config load forbidden.
- Selected node not found.
- Config provider unavailable.
- Generic error with retry.
- Empty config response.
- Empty filtered result.

Errors must be scoped. A config load failure should not hide the node rail, and
a failed refresh of nodes should not leave stale config pretending to be current.

## Accessibility And I18n

- Add Chinese and English messages for navigation metadata, page title,
  description, controls, empty states, copy feedback, and table headers.
- Keep table `aria-label` tied to the visible section title.
- Keep node rows as buttons with `aria-current` or an equivalent selected state.
- Refresh and copy controls need accessible names.
- Search inputs need labels or `aria-label`.

## Performance

The page is operator-triggered and node-scoped.

Performance rules:

- Do not add config data to `GET /manager/nodes`.
- Do not poll config snapshots.
- Do not request config for every node to populate the rail.
- Client-side filtering is acceptable because the backend response is bounded by
  an allowlisted config set.
- Avoid expensive per-row React state; use derived filtered groups from current
  response and query state.

## Testing

Focused frontend tests:

- Navigation and route smoke:
  - `/cluster/node-config` renders under the cluster section.
  - Sidebar includes `配置` immediately before `诊断`.
  - Route metadata eyebrow resolves to `CLUSTER / CONFIG`.
- Page behavior:
  - Defaults to local node when available.
  - Honors `?node_id=...` when valid.
  - Falls back when query node is stale.
  - Loads config only for selected node.
  - Search filters by key, label, and value.
  - Group tabs filter visible groups.
  - Copy current filtered result uses redacted values as returned.
  - Node rail failure and config failure render scoped errors.
- Node detail:
  - `查看完整配置` links to `/cluster/node-config?node_id={node_id}`.

Verification commands:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/lib/manager-api.test.ts src/pages/node-config/page.test.tsx src/pages/nodes/page.test.tsx src/pages/page-shells.test.tsx
cd web && /Users/tt/.bun/bin/bunx tsc -b
cd web && /Users/tt/.bun/bin/bun run build
git diff --check
```

If the Vite build only changes `web/dist/index.html` asset hashes, restore that
generated hash churn before committing source changes.

## Acceptance

- `配置` appears as a standalone menu under `集群运维`, immediately before
  `诊断`.
- `/cluster/node-config` is the canonical full-page config inspection surface.
- Operators can select one node, search/filter its config, and copy the current
  filtered result.
- Sensitive values remain redacted; there is no reveal, edit, save, reload, or
  runtime mutation affordance.
- No backend API, permission, config snapshot semantics, polling behavior, or
  cluster fan-out behavior changes are introduced for this first slice.
- The node detail sheet links to the canonical page for full inspection.
- Focused frontend tests, TypeScript, production build, and whitespace checks
  pass during implementation.
