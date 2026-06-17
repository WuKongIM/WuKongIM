# Web Admin Shell

## Commands

- `bun install`
- `bun run dev`
- `bun run test`
- `bun run build`

## Runtime Scope

The web app provides the authenticated manager shell for WuKongIM operations:

- `/login` authenticates against `POST /manager/login`.
- Protected routes require a valid persisted JWT session.
- `VITE_API_BASE_URL` optionally overrides the default same-origin `/manager/*` base.
- During local Vite development, same-origin `/manager/*` requests proxy to `VITE_MANAGER_API_TARGET`, defaulting to the first `scripts/start-wukongimv2-three-nodes.sh` manager server at `http://127.0.0.1:5311`.
- Container deployments can set `WK_WEB_API_URL` to change the nginx `/manager/` proxy target, defaulting to `http://wk-node1:5301`.
- UI copy supports `en` and `zh-CN`.
- Locale selection order is persisted `localStorage` value -> browser language -> default `en`.
- Users can switch languages from the login page and the authenticated shell topbar.

## Page And API Matrix

| Page | Manager API coverage | Status |
|------|----------------------|--------|
| `/cluster/dashboard` | `GET /manager/overview`, `GET /manager/tasks`, `GET /manager/nodes`, `GET /manager/channel-cluster/summary`, `GET /manager/network/summary` | Implemented |
| `/cluster/nodes` | `GET /manager/nodes`, `GET /manager/nodes/:id`, node lifecycle and scale-in APIs; `?panel=onboarding` also uses node onboarding APIs | Implemented |
| `/cluster/slots` | `GET /manager/nodes`, `GET /manager/slots`, `GET /manager/slots/:id`, slot leader/recovery/rebalance APIs | Implemented |
| `/cluster/channels?tab=overview` | `GET /manager/channel-cluster/summary` | Implemented |
| `/cluster/channels?tab=list` | `GET /manager/channel-runtime-meta`, `GET /manager/channel-runtime-meta/:type/:id` | Implemented |
| `/cluster/channels?tab=unhealthy` | `GET /manager/channel-cluster/unhealthy`, `GET /manager/channel-cluster/:type/:id/replicas`, `POST /manager/channel-cluster/:type/:id/repair`, `POST /manager/channel-cluster/:type/:id/leader/transfer` | Implemented |
| `/cluster/tasks` | `GET /manager/distributed-tasks/summary`, `GET /manager/distributed-tasks`, `GET /manager/distributed-tasks/:domain/:id` | Implemented |
| `/cluster/topology` | `GET /manager/overview`, `GET /manager/nodes`, `GET /manager/slots` | Implemented |
| `/cluster/plugins` | `GET /manager/nodes/:id/plugins`, `GET /manager/nodes/:id/plugins/:plugin_no`, `PUT /manager/nodes/:id/plugins/:plugin_no/config`, `POST /manager/nodes/:id/plugins/:plugin_no/restart`, `GET/POST/DELETE /manager/plugin-bindings` | Implemented |
| `/cluster/diagnostics?tab=trace` | Diagnostics tracking, trace, message, and recent event APIs | Implemented |
| `/cluster/diagnostics?tab=network` | `GET /manager/network/summary` | Implemented |
| `/cluster/diagnostics?tab=controller-logs` | Controller Raft log/status and compaction APIs | Implemented |
| `/cluster/diagnostics?tab=slot-logs` | Slot Raft log and compaction APIs | Implemented |
| `/business/dashboard` | `GET /manager/dashboard/metrics`; optional `GET /manager/users`, `GET /manager/channels`, `GET /manager/system-users` for entry-card counts | Implemented |
| `/business/monitor` | `GET /manager/monitor/metrics`, optional `node_id` filter | Implemented |
| `/business/users` | `GET /manager/users`, `GET /manager/users/:uid`, `POST /manager/users/:uid/kick`, `POST /manager/users/:uid/token/reset` | Implemented |
| `/business/channels` | `GET /manager/channels`, `GET /manager/channels/:type/:id`, `POST /manager/channels`, member list add/remove APIs | Implemented |
| `/business/messages` | `GET /manager/messages`, message retention APIs, channel runtime suggestions | Implemented |
| `/business/system-users` | `GET /manager/system-users`, `POST /manager/system-users/add`, `POST /manager/system-users/remove` | Implemented |
| `/system/permissions` | `GET /manager/permissions` | Implemented |
| `/system/webhooks` | Requires follow-up read/write API design | Placeholder |
| `/system/connections` | `GET /manager/connections`, `GET /manager/connections/:session_id` | Implemented |

## Legacy Redirects

Old bookmarks are kept as `replace` redirects into the redesigned sections:

- Dashboard routes: `/dashboard` -> `/cluster/dashboard`; `/monitor` -> `/business/monitor`.
- Cluster routes: `/nodes`, `/onboarding`, `/slots`, `/tasks`, `/topology`, `/channel-cluster`, `/channel-cluster/list`, `/channel-cluster/unhealthy`, `/channels`.
- Diagnostics routes: `/diagnostics`, `/network`, `/controller`, `/slot-logs`; log redirects preserve existing query parameters such as `node_id` and `slot_id`.
- Business routes: `/users`, `/channels-biz`, `/messages`, `/system-users`.
- System routes: `/settings/permissions`, `/settings/webhooks`, `/connections`.

## Channel Cluster Notes

- P0 read path is implemented for summary, unhealthy pagination, and dashboard health.
- P0.5 safe operations are implemented for replica inspection and `no_leader` repair.
- P0.6 single-channel explicit leader transfer is implemented for active non-leader ISR replicas.
- Replica detail only displays proven runtime values; unknown follower commit/lag values stay `-`.
- Batch leader drain remains hidden until follow-up batch orchestration exists.
