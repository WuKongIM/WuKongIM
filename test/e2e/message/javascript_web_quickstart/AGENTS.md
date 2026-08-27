# JavaScript web quickstart scenario

This scenario proves the published JavaScript web quickstart against a real
single-node cluster and a real Chromium browser.

## Rules

- Keep the Go test responsible for the real `cmd/wukongim` lifecycle, the
  256-Hash-Slot topology override, and isolated loopback addresses.
- Keep application-integration assertions in the quickstart sample's
  Playwright spec. The browser must reach Product HTTP only through the
  localhost BFF and must discover its WebSocket URL through `/route`.
- Cover Alice/Bob bidirectional durable sends, successful SENDACK and receive,
  Bob disconnecting before Alice sends, and Bob reconnecting and recovering
  the offline message through sync.
- Keep this scenario opt-in with `WK_E2E_DOCS_JAVASCRIPT_WEB=1` so the complete
  Go e2e suite does not require npm or Chromium.
- Keep command output bounded but never publish its raw tail or the node log
  tail. On failure retain at most three Playwright PNG screenshots under the
  ignored `tmp/docs-site-e2e/` directory, with each image capped at 2 MiB;
  successful runs remove only their unique run directory.
- `WK_DOCS_GOLDEN_PATH_ATTESTATION_OUTPUT` may point only below the repository
  `tmp/docs-site-e2e/` directory. After the browser scenario succeeds, a clean
  committed worktree may atomically publish the exact runtime receipt there;
  tracked or untracked worktree changes must refuse verification.

## Run

```bash
(cd docs-site/examples/javascript-web-quickstart && npm ci && npx playwright install chromium)
WK_E2E_DOCS_JAVASCRIPT_WEB=1 GOWORK=off go test -tags=e2e ./test/e2e/message/javascript_web_quickstart -count=1 -timeout 10m -p=1 -v
```
