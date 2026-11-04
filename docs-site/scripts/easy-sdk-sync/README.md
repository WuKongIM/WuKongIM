# Web EasySDK documentation upgrade pilot

The daily `easy-sdk-web-docs-sync.yml` workflow proposes compatible released
Web SDK upgrades. It does not publish SDKs or merge documentation PRs. Other
languages still use the reviewed manifest maintenance process.

## Release selection

- Resolve npm `easyjssdk` and require an exact stable version on the current
  major line (the current minor line for a `0.x` package). Never downgrade.
- Bind npm `gitHead` and SHA-512 integrity to the SDK tag, main ancestry and a
  successful push-triggered `publish-npm.yml` run on that tag/source.
- Skip versions already covered by an open or closed
  `codex/easy-sdk-web-<version>` PR. Closing a proposal suppresses retries for
  that version; reopen the original PR if it should be reconsidered.
- Default manual dispatch verifies the current selected package. Set
  `verify_current=false` to check for an upgrade. A PR-triggered run verifies
  the PR's selected package and has no publication authority.

## Validation and proposal

The browser fixture extracts all three TypeScript blocks from each tutorial,
requires the two languages' code to match, and compiles them against an exact
npm consumer. The SDK lock entry must match the discovered version, tarball
and integrity before `npm ci --ignore-scripts` runs. The browser tools have
their own committed lockfile.
On Ubuntu, a credential-free launch probe detects AppArmor user-namespace
restrictions. Only if needed, the job grants `userns` to the exact pinned
Chromium executable through a temporary profile, removed at job cleanup.
Chromium remains sandboxed and the host-wide restriction is unchanged.

The Go E2E suite starts a real 256-hash-slot single-node cluster with Token
authentication enabled. Two isolated Chromium sessions use the tutorial's
actual `EasyChatClient` through a loopback BFF. The acceptance checks sends in
both directions, disconnect cleanup through `/user/onlinestatus`, reconnect
and subsequent delivery, final cleanup, and the complete 10-second connection
deadline against a stalled WebSocket handshake. All clients and processes have
bounded cleanup. The receipt records package/source identities, both tutorial
hashes as one digest, consumer/tool lock digests, Node and Chromium versions.
It is not a claim about multiple nodes, other browsers, or historical soak runs.

After browser acceptance, the read-only job updates the manifest and Changelog,
regenerates navigation, and runs the complete `bun run verify`. Only a plan and
receipt are uploaded. A separate main-branch writer recreates those same three
files from trusted source, compares their hashes, re-reads main, and creates a
version-specific PR through the GitHub API. No npm package or artifact-provided
code executes with write permission. A newer main defers publication until the
next daily check. An interrupted branch creation can be recovered only when
its complete tree and parent still match; differing branches are preserved for
manual inspection. There are no force pushes, automatic approvals or merges.

The repository must allow Actions to create pull requests, while retaining
read-only default workflow permissions. Only the isolated proposal job requests
write permissions; no personal token or App key is required. GitHub may put
[workflow-created PR runs behind approval](https://docs.github.com/en/actions/concepts/security/github_token).
The proposal's own full documentation and browser checks run before creation.
After human review and merge, `docs-pages.yml` publishes the documentation.

## Local reproduction

From the repository root, use an ignored directory for the consumer and receipt:

```bash
VERIFY_CURRENT=true node docs-site/scripts/easy-sdk-sync/cli.mjs discover "$PWD/tmp/easy-sdk-web"
npm ci --ignore-scripts --prefix test/e2e/message/easy_sdk_docs_release
(cd test/e2e/message/easy_sdk_docs_release && PLAYWRIGHT_SKIP_BROWSER_GC=1 npx --no-install playwright install chromium)
node test/e2e/message/easy_sdk_docs_release/prepare.mjs "$PWD/tmp/easy-sdk-web"
WK_E2E_EASYSDK_ARTIFACTS="$PWD/tmp/easy-sdk-web" GOWORK=off go test -tags=e2e ./test/e2e/message/easy_sdk_docs_release -count=1 -timeout=5m -p=1 -v
```

Run from a committed checkout when recording final source-bound evidence. A
local receipt has run ID `local` and cannot authorize a GitHub proposal. The
`apply` command modifies the three proposal files; use it only in an isolated
checkout. `seal` follows a successful complete documentation verification gate.
Failures upload no success receipt and leave the public manifest unchanged.
