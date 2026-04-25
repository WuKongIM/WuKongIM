# Web Admin Shell

## Commands
- `bun install`
- `bun run dev`
- `bun run test`
- `bun run build`

## Scope
The web app now includes the first authenticated manager shell flow:

- `/login` authenticates against `POST /manager/login`
- protected routes require a valid persisted JWT session
- `VITE_API_BASE_URL` optionally overrides the default same-origin `/manager/*` base
- container deployments can set `WK_WEB_API_URL` to change the nginx `/manager/` proxy target, defaulting to `http://wk-node1:5301`
- UI copy currently supports `en` and `zh-CN`
- locale selection order is persisted `localStorage` value -> browser language -> default `en`
- users can switch languages from the login page and the authenticated shell topbar

It still keeps the rest of the admin experience intentionally lightweight:

- no real nodes/slots/overview data fetching yet
- no charts or topology rendering
- no permission-based navigation trimming yet
