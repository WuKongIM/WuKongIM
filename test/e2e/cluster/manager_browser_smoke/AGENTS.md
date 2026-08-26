# Manager browser smoke scenario

This scenario proves the production Manager bundle works through a real
three-node `cmd/wukongim` cluster and a real Chromium browser.

## Rules

- Keep the Go test responsible for real cluster lifecycle and ephemeral
  Manager credentials.
- Keep browser assertions in `manager_browser_smoke.spec.ts` and exercise only
  the public Manager URL; do not start a Vite development server or mock APIs.
- Cover authenticated desktop navigation, localized copy, the not-found route,
  mobile navigation, failed HTTP responses, browser console warnings/errors,
  and uncaught page errors.
- Keep this scenario opt-in with `WK_E2E_MANAGER_BROWSER=1` so the complete Go
  e2e suite does not require a browser installation.

## Run

```bash
(cd web && bun install --frozen-lockfile && bunx playwright install chromium && bun run build)
WK_E2E_MANAGER_BROWSER=1 GOWORK=off go test -tags=e2e ./test/e2e/cluster/manager_browser_smoke -count=1 -timeout 5m -p=1 -v
```
