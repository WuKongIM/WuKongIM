# Web Admin Panel Restructure Design

## Summary

Restructure the WuKongIM web admin panel from a flat operations-only layout into a 5-module architecture that covers both cluster operations and business management. The key addition is a dedicated Channel Cluster module reflecting WuKongIM's per-channel ISR architecture.

## Current State

The panel has 12 pages in 3 navigation groups (Overview, Runtime, Observability). All pages focus on cluster operations. No business management capabilities exist. The existing Channels page only shows `channel-runtime-meta` in a flat list without cluster-level operations.

## Target Navigation Structure

```
📊 总览 (Overview)
  ├── dashboard        — 核心指标仪表盘
  └── monitor          — 实时监控（P1，占位）

🖥️ 全局集群 (Global Cluster)
  ├── nodes            — 节点管理
  ├── slots            — Slot 管理
  ├── onboarding       — 节点扩容
  ├── controller       — Controller Raft
  └── topology         — 拓扑视图

📡 频道集群 (Channel Cluster)
  ├── channel-cluster           — 频道集群总览（健康度面板）
  ├── channel-cluster/list      — 频道列表（Leader/ISR/Replicas）
  └── channel-cluster/unhealthy — 异常频道

👥 业务管理 (Business)
  ├── users            — 用户管理（P1，占位）
  ├── channels-biz     — 频道业务管理（P1，占位）
  ├── messages         — 消息管理
  └── system-users     — 系统用户（P1，占位）

🔧 诊断工具 (Diagnostics)
  ├── diagnostics      — 消息追踪
  ├── network          — 网络诊断
  ├── connections      — 连接管理
  └── slot-logs        — 槽位日志

⚙️ 系统设置 (Settings)
  ├── settings/permissions — 权限管理（P1，占位）
  └── settings/webhooks    — Webhook 配置（P1，占位）
```

## Scope of This Implementation

This spec covers the **framework restructuring only**:

1. Reorganize `navigation.ts` into 5 groups with new icons
2. Add new routes to `router.tsx`
3. Create placeholder pages for new modules (channel-cluster, monitor, users, channels-biz, system-users, settings)
4. Add i18n keys (zh-CN and en) for all new navigation items
5. Rename existing "Channels" page to clarify it's the channel cluster list view
6. Move `connections` from Runtime group to Diagnostics group
7. Move `slot-logs` from Observability to Diagnostics

Pages marked "P1，占位" get a simple placeholder component with title and description. Existing pages remain unchanged.

## Technical Decisions

- **Router structure**: Use nested routes for channel-cluster (`/channel-cluster`, `/channel-cluster/list`, `/channel-cluster/unhealthy`) and settings (`/settings/permissions`, `/settings/webhooks`)
- **Page pattern**: Each new page follows the existing pattern — a `page.tsx` in its own directory under `src/pages/`
- **Navigation icons**: Use lucide-react icons consistent with existing choices
- **No new dependencies**: Framework restructuring only uses existing packages
- **Existing pages untouched**: No functional changes to existing page components, only their navigation grouping changes

## File Changes

### Modified Files
- `src/lib/navigation.ts` — new 5-group structure
- `src/app/router.tsx` — add new routes
- `src/i18n/messages/zh-CN.ts` — add nav keys for new items
- `src/i18n/messages/en.ts` — add nav keys for new items

### New Files
- `src/pages/channel-cluster/page.tsx` — channel cluster overview
- `src/pages/channel-cluster/list/page.tsx` — channel cluster list (migrate from current channels page)
- `src/pages/channel-cluster/unhealthy/page.tsx` — unhealthy channels placeholder
- `src/pages/monitor/page.tsx` — real-time monitor placeholder
- `src/pages/users/page.tsx` — user management placeholder
- `src/pages/channels-biz/page.tsx` — channel business management placeholder
- `src/pages/system-users/page.tsx` — system users placeholder
- `src/pages/settings/permissions/page.tsx` — permissions placeholder
- `src/pages/settings/webhooks/page.tsx` — webhooks placeholder

### Removed Routes (consolidated)
- `/channels` route removed — replaced by `/channel-cluster/list`

## Navigation Group Mapping

| Group | Icon | Items |
|-------|------|-------|
| Overview | LayoutDashboard | dashboard, monitor |
| Global Cluster | Server | nodes, slots, onboarding, controller, topology |
| Channel Cluster | Radio | channel-cluster, channel-cluster/list, channel-cluster/unhealthy |
| Business | Users | users, channels-biz, messages, system-users |
| Diagnostics | SearchCode | diagnostics, network, connections, slot-logs |
| Settings | Settings | settings/permissions, settings/webhooks |

## Placeholder Page Template

Each placeholder page renders:
- PageContainer + PageHeader (existing shell components)
- Title and description from i18n
- A centered "Coming Soon" / "即将推出" state using the existing ResourceState component with kind="empty"
