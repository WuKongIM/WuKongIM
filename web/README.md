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
- Container deployments can set `WK_WEB_API_URL` to change the nginx `/manager/` proxy target, defaulting to `http://wk-node1:5301`.
- UI copy supports `en` and `zh-CN`.
- Locale selection order is persisted `localStorage` value -> browser language -> default `en`.
- Users can switch languages from the login page and the authenticated shell topbar.

## Page And API Matrix

| Page | Manager API coverage | Status |
|------|----------------------|--------|
| `/dashboard` | `GET /manager/overview`, `GET /manager/tasks`, `GET /manager/nodes`, `GET /manager/channel-cluster/summary` | Implemented |
| `/channel-cluster` | `GET /manager/channel-cluster/summary` | Implemented |
| `/channel-cluster/list` | `GET /manager/channel-runtime-meta`, `GET /manager/channel-runtime-meta/:type/:id` | Implemented |
| `/channel-cluster/unhealthy` | `GET /manager/channel-cluster/unhealthy`, `GET /manager/channel-cluster/:type/:id/replicas`, `POST /manager/channel-cluster/:type/:id/repair`, `POST /manager/channel-cluster/:type/:id/leader/transfer` | Implemented |
| `/nodes`, `/slots`, `/onboarding`, `/controller` | Existing cluster manager endpoints | Implemented |
| `/messages`, `/diagnostics`, `/network`, `/connections`, `/slot-logs` | Existing diagnostics and message endpoints | Implemented |
| `/users` | `GET /manager/users`, `GET /manager/users/:uid`, `POST /manager/users/:uid/kick`, `POST /manager/users/:uid/token/reset` | Implemented |
| `/channels-biz` | `GET /manager/channels`, `GET /manager/channels/:type/:id`, `POST /manager/channels`, member list add/remove APIs | Implemented |
| `/system-users` | Requires manager-scoped business APIs or frontend wrapping | Placeholder |
| `/monitor`, `/settings/permissions`, `/settings/webhooks`, `/topology` | Requires follow-up read/write API design | Placeholder |

## Channel Cluster Notes

- P0 read path is implemented for summary, unhealthy pagination, and dashboard health.
- P0.5 safe operations are implemented for replica inspection and `no_leader` repair.
- P0.6 single-channel explicit leader transfer is implemented for active non-leader ISR replicas.
- Replica detail only displays proven runtime values; unknown follower commit/lag values stay `-`.
- Batch leader drain remains hidden until follow-up batch orchestration exists.
