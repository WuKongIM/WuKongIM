# GitHub Actions workflows

Workflows fall into three groups:

- **CI** checks code: `ci.yml` on every pull request and push to `main`,
  `perf.yml` and `compat.yml` nightly or on demand.
- **CD** publishes releases and documentation.
- **Ops** tools drive the paid Alibaba Cloud chat-lifecycle lab. Workflows that
  create cloud resources, change permissions, or spend money require explicit
  user authorization and the applicable budget.

Run the same checks locally with `go run ./cmd/wkcicheck <selector>`; the
selector list in `cmd/wkcicheck/main.go` is the single catalog of named checks.

## Catalog

| File | Display name | Purpose |
| --- | --- | --- |
| `ci.yml` | `CI` | Pull-request and `main` correctness: lint, Go unit, race, scripts integration, E2E, Manager web, demo, documentation and package jobs selected by changed paths, aggregated into the single required check `ci-ok`; nightly full race/integration/E2E |
| `perf.yml` | `Performance` | 500 QPS seams on tmpfs and runner disk with runner-class summaries, same-runner A/B for pull requests labeled `perf`, the ten-minute three-node qualification, the nightly conversation QPS release gate as early warning, and the fixed SEND diagnosis |
| `compat.yml` | `Compatibility` | Nightly native deb/rpm build plus four-distribution install and systemd lifecycle, and released EasySDK Android/iOS/Flutter acceptance |
| `conversation-qps-diagnose.yml` | `Agent Tool - Diagnose Conversation QPS` | Read-only four-CPU AMD64 fixed mixed-load attribution with separate driver/server profiles; never publication evidence |
| `binary-release-publish.yml` | `Release` | One tag-triggered release: conversation QPS gate once, then the immutable multi-platform Docker image and its mirrors, then archives, unsigned deb/rpm and the immutable GitHub Release |
| `docs-pages.yml` | `Safety Automation - Publish Documentation to GitHub Pages` | Verifies and deploys the exact documentation artifact, then optionally refreshes the pre-provisioned Alibaba Cloud CDN |
| `docs-cdn-certificate.yml` | `Safety Automation - Renew Documentation CDN Certificate` | While explicitly enabled, checks daily and renews the public CDN certificate through ACME DNS-01 and Alibaba Cloud OIDC |
| `easy-sdk-web-docs-sync.yml` | `Safety Automation - Propose Web EasySDK Documentation Upgrade` | Checks successful Web npm publications daily, verifies literal tutorials in Chromium, and proposes a bounded documentation PR |
| `chat-lifecycle-rehearsal.yml` | `Agent Tool - Start Chat Lifecycle Rehearsal` | Builds, quotes, acquires, deploys, and hands a full-scale two-hour rehearsal to remote systemd |
| `chat-lifecycle-rehearsal-finalize.yml` | `Safety Automation - Finalize Chat Lifecycle Rehearsals` | While armed, uploads a terminal rehearsal report before Release and reconciles the Lease to zero inventory |
| `chat-lifecycle-formal.yml` | `Safety Automation - Start Fresh Formal Chat Lifecycle` | While armed, consumes an authenticated released rehearsal transition and starts a fresh 96-hour formal Lease |
| `chat-lifecycle-formal-finalize.yml` | `Safety Automation - Finalize Formal Chat Lifecycle Runs` | While armed, collects the same-Lease Soak/capacity/recovery result before Release and zero-inventory proof |
| `chat-lifecycle-stop.yml` | `Agent Tool - Stop Chat Lifecycle Request` | Seals one request-level stop marker and requests coordinated cancellation plus bounded operator-stop finalization |
| `cloud-lease-oidc-setup.yml` | `Agent Tool - Configure Cloud Lease OIDC Roles` | Reconciles and live-verifies the three workflow-conditioned Cloud Lease roles |
| `cloud-lease-provision.yml` | `Agent Tool - Provision Cloud Lease` | Quotes or explicitly acquires one generic Alibaba Cloud Lease |
| `cloud-lease-observe.yml` | `Agent Tool - Inspect Cloud Lease` | Reconstructs exact Lease inventory through the read-only Observer role |
| `cloud-lease-analyze.yml` | `Agent Tool - Analyze Chat Lifecycle Cloud Lease` | Authenticates one retained chat-lifecycle handoff, proves live Lease inventory, and brokers one bounded Analysis MCP session |
| `cloud-lease-release.yml` | `Safety Automation - Release Cloud Leases` | While armed, releases one exact Lease and runs the protected 15-minute expired/cleanup-pending repository sweep |
| `cloud-deployment-bundle.yml` | `Agent Tool - Build Cloud Deployment Bundle` | Builds and seals one procurement-independent offline Ubuntu four-host payload |
| `cloud-deployment-activate.yml` | `Agent Tool - Activate Cloud Deployment` | Installs and gates one exact offline bundle on an active four-host Lease |

## CI

`ci.yml` is the only pull-request gate. Branch protection requires exactly one
check, `ci-ok`; it fails when any job fails or is cancelled and treats skipped
jobs as passing. `scripts/ci/changes.sh` maps changed paths to areas:

| Job | Runs when | Content |
| --- | --- | --- |
| Lint | always | gofmt, go vet, `go mod tidy -diff`, FLOW and Skill contracts, actionlint |
| Go unit | Go changed | `go test ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/...` |
| Go race | Go changed | messaging hot-path race tests |
| Scripts integration | Go changed | `go test -tags=integration ./scripts/...` |
| E2E | Go changed | one `e2e` server binary: ordinary-history migration, cross-node delivery, self chat, and the three-node 500 SEND/s correctness/drain smoke |
| Manager web | Manager front end or back end changed | `wkcicheck web` plus the real three-node Chromium smoke |
| Chat demo | `demo/` changed | `wkcicheck demo` |
| Documentation | docs, Changelog or any Go changed | `wkcicheck docs`; golden-path paths add `wkcicheck docs-integration` |
| Native package | packaging, goreleaser or CLI entrypoints changed | deb/rpm snapshot build, metadata inspection, Ubuntu install |

Documentation contracts compare the site with server routes, so every Go
change runs them. Without this, a route change once broke documentation
publishing on `main` for six days.

The nightly schedule additionally runs race on every listed package,
`-tags=integration ./internal/... ./pkg/...` and every E2E suite. A nightly
failure opens or updates one `[nightly] ... failing` Issue.

## Performance

GitHub-hosted runners differ between allocations. Fourteen days of 500 QPS
evidence (388 runs) showed failures concentrated on AMD EPYC 9V74 runners,
whose disk writes were about 3.5 times slower (median channel-append p99
164 ms versus 7 ms on AMD EPYC 7763), while CPU pressure was not higher.
`perf.yml` therefore detects regressions instead of certifying capacity:

- `scripts/run-500qps-seam.sh` accepts `WK_PERF_DATA=tmpfs` to keep fixture
  data in `/dev/shm`, removing disk variance; `disk` keeps the old behavior.
- `scripts/perf/summarize_seam.py` records allocations and bytes per
  operation (hardware-independent), window p99, CPU model, disk write latency,
  IO pressure and data footprint in each job summary.
- A pull request labeled `perf` runs `scripts/perf/ab.sh`: base and head
  alternate three times on one runner and `compare_ab.py` reports ratios.
- Observation mode: A/B regressions are reported but do not fail, and runner
  disk runs and the ten-minute qualification never fail the workflow. Enable
  enforcement (`WK_PERF_ENFORCE=1`) after nightly data calibrates limits.

Capacity (sustaining 500 QPS within 400 ms p99 on real disks) needs fixed
hardware, such as a rented fixed-specification instance, and is not claimed
by hosted-runner results.

## Compatibility

`compat.yml` runs nightly, on demand, and for pull requests that change its
own suites. The native package job builds unsigned deb/rpm previews with
GoReleaser, validates temporary signed repository metadata, installs on
Ubuntu 24.04, Debian 12, Rocky Linux 9 and AlmaLinux 9, then a matrix boots
each distribution with real systemd and exercises the operator-owned
lifecycle. EasySDK jobs install the pinned released Android, iOS and Flutter
packages and prove bidirectional messaging against the current server. The
signed public package channel remains owned by `WuKongIM/packages`.

## Documentation Pages

`docs-pages.yml` is the sole documentation publisher. A merge to `main` that
changes `CHANGELOG.md`, `docs-site/**`, or its bounded publication files starts
it automatically. A successful tag-initiated
`Safety Automation - Publish WuKongIM Binaries` run also starts it through
`workflow_run`; `workflow_dispatch` is reserved for first publication,
recovery, and the bounded custom-domain migration mode. The build job has
read-only repository access, installs the locked Bun dependencies, runs
`bun run verify`, and uploads only the resulting `docs-site/out` directory. The
deploy job receives only the Pages and OIDC permissions required by GitHub's
deployment API.

For a release-triggered build, the Workflow checks out the upstream run's exact
source SHA and fails unless the run came from this repository, completed a tag
push successfully, names a strict release candidate, remains reachable from
`main`, has a tag that peels to the same commit, and already owns a matching
non-draft immutable GitHub Release. `DOCS_RELEASE_TAG` then requires the latest
exact version heading in the tagged root `CHANGELOG.md` to equal that release.
The MDX pipeline derives the container tag by removing the leading `v` and
replaces the reviewed current-release placeholders at build time. Historical
version references stay literal. The release source executes only in the
read-only build job; the Pages deployment executes no checked-out source, and
the CDN refresh retains the protected default-branch checkout.

A manual migration run may set `expected_pages_domain` to exactly
`docs.githubim.com` or `origin-docs.githubim.com`. The Workflow finishes the
build and upload before its deploy job waits for the Pages API to expose that
exact domain. This lets an operator stage the verified artifact before the
single external custom-domain mutation. A non-empty migration input suppresses
CDN refresh for that run. Changing the Pages setting, receiving REST `204`, or
seeing an approved certificate never counts as content readiness by itself.

The repository Pages source must remain `workflow`, and the unchanged
`github-pages` Environment is the content-publication boundary. In the planned
CDN topology, GitHub Pages owns `origin-docs.githubim.com` and remains the sole
content origin. Alibaba Cloud CDN serves the canonical public
`docs.githubim.com` domain and sends HTTPS origin requests with
`origin-docs.githubim.com` as the address, Host, and SNI. GitHub's repository
Pages Settings/API is the sole custom-domain authority. The export retains
`.nojekyll` but deliberately contains no `CNAME`.

After every successful Pages deployment, a read-only verification job requires
the Pages API to report the exact current domain, workflow build, verified
ownership, HTTPS enforcement, and approved origin certificate. It then bypasses
public CDN DNS with `curl --connect-to` and performs complete GETs for the root,
both locale roots, one deep page, and the search index. Only after that direct
origin gate succeeds may an isolated refresh job assume the refresh-only
Alibaba Cloud role from the `docs-cdn` Environment. The refresh job is skipped
unless repository Variable `DOCS_CDN_ENABLED` is exactly `true`, and is also
skipped for a migration-mode run. Once enabled, a missing or invalid binding
fails before OIDC exchange. It refreshes a bounded set of stable public URLs;
it does not create provider resources, change DNS, replace CDN settings, or
broadly purge content-addressed assets.

`docs-cdn-certificate.yml` is a separate certificate safety automation. Its
scheduled and manual paths are also inert while `DOCS_CDN_ENABLED` is disabled.
When enabled, it uses the `docs-cdn-certificate` Environment, a distinct
certificate-rotation OIDC role, and the Environment Secret
`DOCS_ACME_ACCOUNT_BUNDLE_B64` to renew the Let's Encrypt edge certificate for
`docs.githubim.com`. GitHub Pages independently owns and renews the origin
certificate for `origin-docs.githubim.com`; neither private key crosses that
boundary.

These workflows consume repository Variables `DOCS_CDN_DOMAIN`,
`DOCS_CDN_CNAME`, `DOCS_CDN_PUBLIC_ROUTE_MODE`,
`DOCS_CDN_OIDC_PROVIDER_ARN`, `DOCS_CDN_OIDC_AUDIENCE`,
`DOCS_CDN_REFRESH_ROLE_ARN`, `DOCS_CDN_CERTIFICATE_ROLE_ARN`, and
`DOCS_ACME_EMAIL`. `DOCS_CDN_CNAME` is the exact Alibaba-assigned CDN CNAME
`docs.githubim.com.w.kunlunaq.com`, without a trailing dot.
`DOCS_CDN_PUBLIC_ROUTE_MODE` is a strict enum:

- `github-pages-precutover` requires every fixed public resolver's direct
  CNAME answer for `docs.githubim.com` to be `wukongim.github.io` and skips the
  Alibaba edge-certificate comparison;
- `alibaba-cdn` requires every direct answer to equal `DOCS_CDN_CNAME`, then
  requires the public endpoint to serve the exact certificate read back from
  the Alibaba API with a trusted chain.

Alibaba's `DomainCnameStatus` is validated and recorded only as provider API
diagnostics. It does not establish the public route; that decision uses the
explicit mode and direct public CNAME answers. The Variables' presence is not
evidence that DNS, CDN, RAM, delegated ACME validation, or GitHub Pages
custom-domain setup is complete. Those are administrator-owned external
operations and must remain disabled until the cutover prerequisites and
rollback snapshot in the
[documentation CDN runbook](../../docs/superpowers/runbooks/docs-alibaba-cdn.md)
have been verified.

## Web EasySDK documentation upgrades

`easy-sdk-web-docs-sync.yml` polls npm hourly at minute 17, after the SDK's
`publish-npm.yml` succeeds. It re-reads npm integrity/gitHead, the exact SDK tag,
main ancestry, and the successful publication run. Unchanged versions and
existing open or closed proposals skip expensive checks; prereleases and major
compatibility changes cannot advance the tutorial. Manual dispatch defaults to
verifying the currently documented package without creating a PR. Set
`verify_current=false` to discover a new compatible release. PR events validate
only; they never reach the writer.

The read-only verification job installs a locked browser toolchain and an exact
integrity-checked npm consumer with lifecycle scripts disabled, compiles the
literal bilingual tutorial, and runs Chromium against a real Token-authenticated
256-hash-slot single-node cluster. It verifies bidirectional messaging, public
online-route cleanup, reconnect and a bounded stalled WebSocket handshake.
`bun run verify` then gates the proposed document output. Only the bounded plan
and identity receipt are uploaded; browser logs, credentials and payloads are not.
An Ubuntu launch probe conditionally grants user namespaces to the exact pinned
Chromium executable through a temporary AppArmor profile. Job cleanup removes
the profile; the browser sandbox and host-wide kernel restriction stay enabled.

An isolated main-branch job receives `contents: write` and `pull-requests: write`.
It executes only checked-out trusted control code, regenerates the three allowed
files (SDK manifest, generated navigation and Changelog), verifies their hashes
against the receipt, and re-reads main before creating a version-specific PR.
It never force-pushes, approves or merges. A changed base defers to the next poll;
failed verification leaves the current tutorial unchanged. GitHub's repository
setting allowing Actions to create PRs must be enabled; default workflow access
remains read-only. GITHUB_TOKEN-created PR workflows may require a maintainer to
approve their execution. Human merge invokes the existing `docs-pages.yml`.

See [the maintenance runbook](../../docs-site/scripts/easy-sdk-sync/README.md)
for local reproduction, duplicate handling and the scope of the receipt.

## Conversation QPS diagnosis

`conversation-qps-diagnose.yml` takes an exact product SHA on main history or a
main-descended candidate branch, and optional
previous binary digest. It builds that clean source with Go 1.25.11 before
checking out the diagnostic harness separately. A supplied digest mismatch
aborts; it must not silently substitute a different binary. The read-only
Ubuntu 24.04 job requires four visible AMD64 CPUs and records CPU model, kernel,
Go build identity, host pressure/throttling counters, driver counters, and
per-node RPC/hydration metrics. Three successive 60-second windows reuse the
unchanged release mixed preset (200/60 QPS, eight workers each). Separate
ten-second CPU/allocation profiling does not count as performance evidence.
Every rejected window stays in the report; unexpected errors and runtime or
membership mutations abort. All artifacts, including the binary, are retained
for 90 days. There are no publishing permissions, threshold controls, automatic
triggers, retries, or production targets. Completion means evidence collection,
not a passing gate. An optional exact `comparison_sha` builds a second clean
binary and measures it first on the same host, using the identical harness and
preset with a fresh cluster. Its three windows, profiles and binary identity
remain under `comparison/`, including rejected windows. This bounds CPU-model
variation; sequential ordering still does not remove every host/time effect.
Both products retain the same 12-minute test deadline within the existing
40-minute job. Dispatch once per evidence-backed comparison; do not rerun
until green. Optional `run_release_matrix=true` also runs all 20 unchanged
release endpoint results on a fresh fixture, with the existing v2 receipt
validator. It requires product SHA to equal harness SHA and has no publishing
authority. The job deadline is 40 minutes; each diagnostic and gate test keeps
its original 12/18-minute deadline. Actual publishers still require their own
exact-tag gate.

## Release

`binary-release-publish.yml` (display name `Release`) is the only publisher.
A new strict SemVer `v*` tag starts it; manual recovery must be dispatched from
that same tag ref:

```bash
gh workflow run binary-release-publish.yml --repo WuKongIM/WuKongIM --ref "$tag" -f version="$tag"
```

The file name is part of the release identity: `WuKongIM/packages` verifies
binary build provenance against `.github/workflows/binary-release-publish.yml`,
so do not rename it without changing that repository first. Release delivery
also requires the independently signed APT/RPM publication and public
exact-version client checks in
[`RELEASING.md`](../../docs/development/RELEASING.md); this workflow does not
complete that stage and receives no signing credential.

One run executes three jobs in order:

1. **`conversation-qps`** runs `scripts/release/conversation-qps-gate.sh` once
   on the exact tag, with cluster data on tmpfs so hosted-runner disk speed
   does not decide the result. Thresholds live in
   [`profile.json`](../../test/e2e/message/conversation_qps/profile.json) and the
   report must pass `scripts/validate-conversation-qps-report.jq`. The
   three-node mixed stress window saturates a 4-vCPU hosted runner, so only its
   errors, fences and allocation ceiling gate; its throughput, drops and P99
   are recorded (nightly in `perf.yml`). There is no bypass input; diagnose a failure from its 90-day evidence instead of
   rerunning until green. `perf.yml` runs the same gate nightly on `main`.
2. **`docker`** (Environment `docker-publish`) validates the tag (strict SemVer,
   reachable from `origin/main`, valid `CHANGELOG.md` section), builds
   `linux/amd64,linux/arm64` candidates, blocks on any Critical/High Trivy
   finding and on the embedded-Prometheus probe, pushes the canonical
   `ghcr.io/wukongim/wukongim` image, attests provenance and per-platform
   CycloneDX SBOMs, and copies the exact digest to `docker.io/wukongim/wukongim`
   and `registry.cn-shanghai.aliyuncs.com/wukongim/wukongim`. Exact tags are
   never overwritten; a stable tag advances `MAJOR.MINOR`, `MAJOR` and `latest`
   only when it is the highest stable version in that range. In recovery it
   reuses the canonical digest and fills only missing mirrors; when every
   registry is already complete it verifies and succeeds, so a binary-only
   recovery can rerun the whole workflow.
3. **`binaries`** (Environment `binary-publish`) builds deterministic
   Linux/macOS archives (both `wukongim` and `wkcli`) and unsigned Linux amd64
   `.deb`/`.rpm`, one checksum file and one signed provenance statement, checks
   that all three registries serve the Docker job's digest, and renders the
   Release body from the exact `CHANGELOG.md` section plus machine facts. It
   creates a draft, uploads and byte-verifies every asset, publishes the draft
   once, then verifies GitHub sealed it as an immutable Release. Any existing
   published Release fails closed; an incomplete one needs a new tag.

Repository administrators configure:

- Environment `docker-publish`: Variables `DOCKERHUB_USERNAME`,
  `ALIYUNHUB_USERNAME`; Secrets `DOCKERHUB_TOKEN`, `ALIYUNHUB_TOKEN`; deployment
  limited to trusted version tags; no reviewer.
- Environment `binary-publish`: deployment limited to trusted version tags; no
  reviewer.
- Repository-level immutable Releases enabled. The Actions token cannot read
  that setting, so verify it before pushing a release tag.

For a user-visible pull request, add the release note under `Unreleased` in
`CHANGELOG.md`; before tagging, check the exact section:

```bash
awk -v version=v3.0.0-beta.5 -f scripts/extract-release-notes.awk CHANGELOG.md >/dev/null
```

## Direct local chat-lifecycle repair

The diagnostic repair loop is intentionally not a GitHub Action. Codex runs
`.agents/skills/wukongim-chat-lifecycle` from the operator's local repository
and uses `scripts/chat-lifecycle/direct-lab.sh` to build an exact committed
candidate, Quote and acquire one temporary Alibaba Cloud Lease, deploy over
SSH, stop stalled traffic, collect bounded evidence, and redeploy later
candidate generations on the same hosts. Only the request-scoped `stop`
command accepts cleanup, and only after it stores an exact selector-bound
zero-inventory proof. The generic expired-Lease sweep remains an independent
safety backstop; it is not the repair control plane.

## Cloud Lease identity and lifecycle

The Cloud Lease identity is separate from the retired Cloud Simulation
identity. One local administrator-authenticated setup command preserves
unrelated Environment settings, removes human-review requirements from the
four unattended Environments, and configures the repository OIDC subject. The
setup Workflow then uses the existing complete Alibaba AccessKey Secret pair
only when the seven non-secret binding Variables are absent or a forced repair
is requested. It creates no Lease infrastructure and leaves those Secrets
untouched.

Successful setup requires live OIDC exchange plus exact sole-policy, one-hour
session, setup-subject, and ordinary-workflow-subject verification for
CloudLeaseProvisioner, CloudLeaseObserver, and CloudLeaseReleaser. Ordinary
Quote/Acquire, Inspect, and Release/Sweep tools then use only their corresponding
short-lived role. Deployment uses `cloud-deployment`, receives no `id-token`
permission, and has no Alibaba credential. See
[`docs/superpowers/runbooks/cloud-lease-identity.md`](../../docs/superpowers/runbooks/cloud-lease-identity.md).

Every paid GitHub Acquire enables the generic Release schedule in a separate
credential-free job before the Provisioner Environment can run. The direct
local repair laboratory enables the same backstop before Acquire, then waits
for any older Release pass to become terminal and seals it enabled again after
the active Receipt exists. A scheduled provider sweep may disable itself only
when its typed result proves zero examined, released, pending, and failed
repository inventory and a separate no-provider-credential job proves every
protected GitHub Lease producer is terminal. Exact cleanup helpers always
re-enable the Workflow before dispatch. Consequently an idle repository has no
15-minute Cloud Lease run, while a new paid Lease cannot depend on a disabled
cleanup backstop.

## Cloud Deployment offline bundle

`cloud-deployment-bundle.yml` runs before any Cloud Lease Quote or Acquire and
has only repository read permission. Its `request_id` is correlation-only and
does not change bundle content. It separates the trusted Workflow control
SHA from an immutable product SHA reachable from `main`, builds both frontend
bundles and all Linux AMD64 binaries on the runner, verifies checksum-pinned
native dependencies, and publishes a content-addressed Ubuntu 24.04 payload.
No Lease identity, cloud credential, runtime secret, or host address enters the
bundle. See
[`docs/superpowers/runbooks/cloud-deployment-bundle.md`](../../docs/superpowers/runbooks/cloud-deployment-bundle.md).

## Cloud Deployment activation

The Action and local repair both invoke `scripts/cloud-deployment/deploy.sh`.
It owns activation order, bounded readiness retries, and the typed outcome;
callers retain provenance validation, credential ownership, and workload start.
Behavioral tests execute this same entry with fake process/SSH adapters.

`cloud-deployment-activate.yml` has repository and Artifact read permission but
no OIDC or provider credential. It authenticates both caller-selected Artifact
runs as successful executions of the exact protected workflows on `main`,
builds trusted local validators before executing bundle code,
validates an active Lease Receipt, derives a WuKongIM Deployment Plan, transfers
one verified bundle through the public load node to three private service
nodes, activates native non-restarting infrastructure units while leaving the
stage-specific coordinator dormant, and emits either a typed Deployment
Receipt or bounded stable failure. Manager/Demo plaintext credentials exist
only in the deployment process and the request-scoped local Codex state; the
Artifact carries an authenticated sealed box addressed to the exact diagnostic
Ed25519 identity supplied by the paid top-level workflow.
Each Lease also gets a fresh Deployment/monitor Ed25519 identity. Setup stores
only its long-lived wrapping public key as a repository Variable and the
matching wrapping private key as a `cloud-deployment` Environment Secret. The
top-level workflow generates and seals the Lease private key against the Run
Plan's request/Lease/source/Plan/expiry identity before paid Acquire. It accepts
an active Receipt only when those values match the pre-sealed envelope exactly,
so key parsing or sealing errors cannot create billable resources and no local
sealing step separates a valid Acquire from Deployment. Deployment and
finalizers open the identity only after the same checks, and the finalizer
deletes the short-lived handoff Artifact after zero inventory is retained
separately. No standing deployment SSH private key is authorized on cloud
hosts.
The two upstream runs need not share the Deployment Action's head SHA: long
bundle builds remain valid while `main` advances. The Lease provenance still
binds the immutable source and bundle digest, and the sealed bundle control SHA
must equal its authenticated producer run.
Only the top-level orchestrator may decide to Release or acquire a fresh Lease.
See
[`docs/superpowers/runbooks/cloud-deployment-activate.md`](../../docs/superpowers/runbooks/cloud-deployment-activate.md).

## Chat lifecycle full-scale rehearsal

`chat-lifecycle-rehearsal.yml` is the paid top-level Agent Tool. Invoke it only
after the user gives the exact paid-run authorization and required cloud
identity/deployment credentials are configured. Create exactly one open, unlocked
`[chat-lifecycle] <request_id>` Issue before dispatch. Before bundle construction,
Quote, key generation or Acquire, orchestration calls the read-only
`comment-request-issue.sh --preflight` and retains `issue-preflight.json`. A
missing, ambiguous, closed, locked or incompletely observed Issue stops startup
without buying resources. This check does not prove future comment-write access
or prevent concurrent Issue edits; runtime failure cleanup remains required.
Both startup workflows attempt to retain that single receipt for eight days in
an independent preflight Artifact. A missing receipt creates no Artifact; an
upload failure is visible but cannot change the original orchestration outcome.
Preflight Artifacts never establish a handoff or authorize further procurement.
The required operator inputs remain `source_sha`, fixed operator `tangtaoit`, one
request-scoped Codex diagnostic Ed25519 public key, and `request_id`. The optional
`released_preclock_run_id` is an explicit start decision for one already released
pre-clock failure, not an automatic retry. The optional string
`request_budget_limit_micros` defaults to `1500000000` CNY micros and may only
lower a new request's ceiling. It must be a canonical decimal integer greater
than `150000000` and at most `1500000000`; the operational stop is exactly that
ceiling minus `150000000`. Empty, signed, padded, fractional, exponential or
out-of-range input is rejected before bundle/key generation or Quote. A lower
ceiling cannot accompany `released_preclock_run_id`; the existing continuation
proof remains bound to the original CNY 1,500/1,350 envelope. Infrastructure,
duration, workload, and retry values come only from the reviewed repository Run Plan. It builds the immutable bundle, obtains a read-only Quote, acquires one
12-hour AutoRelease Lease by consuming that exact admitted Quote, activates the deployment, and starts the dormant rehearsal
unit. The runner exits after the remote `run-start.json` proves 10,000 full
syncs and acceptance of the first full 2,000 SEND/s grant. The remote rehearsal
uses the same sealed five-second accrued-cost and one-hour expiry-reserve guard
as formal execution, with a two-hour active-duration admission requirement.
Before paid Acquire, it seals the generated Deployment/monitor private key with
the Lease ID, Plan digest, source SHA, and expiry already fixed by the Run Plan.
The acquired Receipt must equal that identity before Deployment dispatch.

A lower request ceiling travels in the existing Run Plan/Lease budget fields.
The formal starter derives it from the authenticated rehearsal Run Plan and
Receipt; it never falls back to the default when these values are absent or
conflicting. Both finalizers compare terminal Run Plan, Receipt and DeploymentPlan
budgets after cleanup, preserving the same cap through formal execution. The
remote `wkcli` guard enforces that admitted stop during rehearsal, soak, capacity
and recovery. The bundle still builds `wkcli` from the exact product source: an
older product revision whose guard only accepts the default envelope cannot run
a lower-budget request merely by updating control code.

This input does not authenticate costs from other requests, clear stop markers,
allow another continuation, or grant procurement authority. If an operator
reserves prior costs outside a new request, the new hard cap and stop must both
be lowered by that reserve under an explicit cumulative authorization. A Quote
reserve is not a bill; unknown charges remain unknown. No new cross-request
ledger or automatic paid restart is introduced.

A released pre-clock continuation uses the same request, source and diagnostic
public key. Before bundle/key generation or Quote, the read-only helper requires
the predecessor and current GitHub API runs to share the same numeric Workflow,
protected `main` producer, repository and attempt 1, with current run number
exactly predecessor plus one. The predecessor must be an original rehearsal-1
with zero prior commitment, no continuation sidecar, and no accepted run-start,
measured result or known runtime/correctness terminal. An unknown historical
readiness cause remains unknown. A missing, nonadjacent or rerun producer is not
repairable by changing the run number or starting another request.

The helper authenticates exact original cleanup, Acquire and Release Artifact
IDs, ZIP sizes/digests, immutable control ancestry, public Plan/Receipt/Quote,
both bootstrap identities, and all twelve account/selector-bound zero scopes.
Zero must follow Lease creation and the actual Release step start. GitHub step
completion timestamps have second precision, so their upper bound includes only
that final second; this does not relax the start bound. Unsafe ZIP members,
incomplete authority, missing bytes or unexplained cost coverage reject startup.
It supports only the fixed four-host 12-hour rehearsal shape. The full admitted
Quote is reserved after verifying that its host, disk, bandwidth/traffic and EIP
retention envelope covers the interval from Quote to observed zero. The reserve
is not an invoice or a claim that actual charges are known. The CNY 1,500 hard
limit and CNY 1,350 stop remain cumulative; callers cannot supply a reserve value.

Admission materializes exactly one rehearsal-2 Lease with the authenticated
reserve; it never loops over paid attempts. An existing authenticated same-request
cleanup/complete producer rejects an omitted predecessor before procurement. This
guard reuses the complete bounded Artifact inventory; it cannot recover expired
or deleted history and is not a durable cross-request ledger. Stop markers and the
existing paid concurrency and cleanup guards remain effective. Admission receipts
are independently retained for eight days without becoming handoff evidence.

`continuation/proof.json`, its digest-bound manifest and selected public evidence
travel through the remote handoff and the rehearsal/formal terminal artifacts.
Consumers reauthenticate the original rehearsal producer and exact source bytes,
even when the consumer is a later scheduled run and the producer has completed.
The zero-prior first-start path remains valid without a sidecar. Formal advancement
adds the new rehearsal cost to the inherited reserve. Validation failure blocks
qualification/new procurement only: already-owned Leases are still released and
the zero proof is published first. Public evidence retention for 90 days does not
extend the original child Artifact authority (currently seven days); expired or
missing Acquire/Release authority rejects continuation or qualification.

The helper reads one API response/JSON document up to 2 MiB, one Artifact ZIP up
to 16 MiB, at most 128 members/64 MiB expanded per ZIP, and the reused inventory
up to 20,000 entries/64 MiB. Only an allowlist of typed public JSON is persisted;
no private identity, decrypted credential, raw journal or arbitrary archive entry
is copied into the sidecar. Each exact GET has a 60-second bound and no retry.

Deployment/readiness failure retains the exact acquired Lease rather than
buying replacement hosts. The orchestrator publishes typed repair state on the
tracking Issue and waits for a distinct protected-`main` revision carrying the
exact `Chat-Lifecycle-Repair: <request_id>` commit trailer. It then reuses the
same Lease Artifact, immutable bundle Artifact, diagnostic recipient, and
encrypted per-Lease deployment identity when it invokes the Deployment Action
again. Each control SHA is tried at most once. The loop releases to exact zero
inventory when operator stop, the shared CNY 1,350 operational stop, immutable
expiry/stage reserve, or safe-control loss ends the window. It never substitutes
a changed product source or bundle into the acquired Lease; such a repair is
terminal and needs a separately authorized run after cleanup. Before the
workload clock starts, the orchestrator captures a global journal cursor so a
unit's first-ever start is covered, then reads only that unit's bounded terminal
summary after the cursor. The summary carries the closed observer reason when
observation caused termination: recognized non-preflight coordinator codes are
runtime/correctness failures and release immediately, while preflight or
unrecognized evidence remains in the bounded deployment/readiness repair path.
Before terminating a unit after preparation fails, orchestration writes a
per-generation `pre-clock-generation-*/diagnostic.json` with the actual exit
branch, command/classifier statuses and observed deadline instants. Additional
systemd properties and closed summary codes are diagnostic only; the original
ActiveState/Job and summary classifiers still decide control flow. Unknown or
missing fields remain unverified. Raw summaries, stdout and stderr are not
archived: `summary.json` and `unit.json` expose only fixed fields plus byte counts
and hashes; shell-captured stdout has its trailing newlines removed. Stderr is
drained with constant memory and a 65-second collector bound around the original
60-second SSH command. Diagnostic write failures cannot prevent cleanup. The
existing terminal receipt retains the coordinator code without the raw summary.
Runtime or correctness failure is never retried. `chat-lifecycle-rehearsal-finalize.yml` discovers handed-off runs,
uploads a terminal report or bounded failure diagnostics, and only then invokes
Release until `zero_inventory == true`; a cleanup Artifact is the terminal
proof. The two-hour result can be `rehearsal_pass`, never formal `pass`.
The paid rehearsal Workflow enables this finalizer schedule before orchestration
can Acquire a Lease. After every pass, a separate always-running disarm job
repeats authenticated handoff discovery and checks all non-terminal protected
rehearsal producer runs. It disables future finalizer triggers only when no
producer can still publish a handoff and every published handoff has exact
zero-inventory proof. Any incomplete inventory, producer, or Workflow-state
observation fails closed by leaving the schedule enabled. Global handoff
discovery exhausts at most 20,000 retained repository Artifacts, so unrelated
Artifact volume above the former 5,000-item bound cannot strand the schedule
while the inventory remains explicitly bounded. The Stop Action also
enables the finalizer before its exact request-scoped dispatch. Consequently an
idle repository has no recurring rehearsal-finalizer runs, while paid
orchestration, cleanup continuation, and report rescue retain the ten-minute
safety retry.
An inspectable non-success first stops worker traffic and publishes an
authenticated `diagnosis_window/v1` marker. Scheduled finalization preserves
the first stable failure time for at most two hours so the run-scoped Codex
monitor can invoke read-only cloud analysis; disk, budget, expiry, unreachable
hosts, and operator stop shorten that window. Terminal Artifact upload is
retried by the ten-minute finalizer schedule for at most two additional hours,
then provider Release proceeds even if GitHub Artifact storage is unavailable.
After each Release returns exact zero inventory, the rehearsal finalizer keeps
a 90-day stage-complete Artifact and carries the same authenticated evidence in
the fresh-formal transition. The formal handoff preserves that released
rehearsal evidence, so the final 90-day `chat-lifecycle-formal-complete-*`
Artifact contains rehearsal, hour-24/hour-72 formal, capacity/recovery, both
cost stages, and final cleanup proof under one manifest. Provider billing is
explicitly marked pending/unresolved until delayed provider-reported data is
available; it is never fabricated from the conservative Quote.
All five Chat Lifecycle orchestrator/finalizer/stop workflows also advance the
exact `[chat-lifecycle] <request_id>` Issue with idempotent hidden state markers.
While a stage remains active, scheduled finalizers publish at most one
conservative aggregate-cost update per 30-minute bucket. The formal finalizer
closes the Issue only after the combined final Artifact upload succeeds with
exact zero inventory, so progress remains durable when no local Codex monitor
is running.
The scenario-wide concurrency lock permits only one paid Chat Lifecycle
orchestrator at a time, and startup also refuses procurement while any prior
authenticated rehearsal or formal handoff or cleanup-pending Lease lacks its selector-bound zero
proof. A handed-off Lease is released immediately if Artifact
publication fails. Every immediate cleanup owner performs one complete
provider-bounded Release pass; the finalizer retries on its next schedule, and
the independent 15-minute Cloud Lease Sweep reconciles cleanup-pending or
expired inventory even if an owning job is canceled.

`chat-lifecycle-stop.yml` is the only manual request-stop entrypoint. It first
uploads an authenticated request-level stop marker, which makes formal
transition discovery fail closed, then dispatches both stage finalizers with
the fixed operator-stop authorization. Before a handoff exists, the paid
orchestrator observes that marker, cancels only its exact current child, waits
for the child to become terminal, and retains ownership until exact Release;
the Stop Action must not hard-cancel that cleanup owner. After handoff, the
stage finalizer gives a live coordinator one graceful signal and at most ten
minutes to publish terminal evidence before cleanup continues. Every scheduled
finalizer pass independently reauthenticates the durable marker, so a marker
and handoff publication race cannot lose the stop intent.

A passing rehearsal finalizer releases the rehearsal Lease first, authenticates
its selector-bound zero proof, and publishes one `formal_transition/v1` bound to
the same source SHA, bundle digest, request, and aggregate budget ledger. That
ledger uses exact Lease creation-to-zero-inventory time, observed non-loopback
transmit bytes rounded upward to GiB, and the full retention-risk allowance;
it does not commit the whole 12-hour rehearsal Quote.
`chat-lifecycle-formal.yml` is an internal scheduled safety continuation, not a
second public operator surface. It consumes at most one unspent transition,
refuses procurement if either stage still has active inventory, reuses the
exact original bundle, and acquires a completely fresh 96-hour formal Lease.
The rehearsal finalizer enables this continuation before it can publish a
formal transition. An always-running disarm job disables future continuation
triggers only after the complete bounded Artifact inventory contains no
authenticated unconsumed transition and no protected rehearsal finalizer can
still publish one. Formal transition discovery shares the 20,000-Artifact
bound and four-attempt per-page retry used by handoff discovery. Before formal
orchestration can Acquire, the
continuation enables the formal finalizer; that finalizer disables itself only
after no authenticated formal handoff lacks zero-inventory proof and no
protected formal producer remains active. Operator stop re-enables the exact
formal finalizer before dispatch. These state transitions remove idle formal
schedules without weakening transition or cleanup recovery.
Remote `wkbench-formal.service` owns the uninterrupted 72-hour Soak, hour-24
qualification, at-most-eight-hour aged-data capacity staircase, and 30-minute
2,000-SEND/s recovery in one `wkcli bench formal-chain` process with the same
worker fence, generation, observer, lifecycle proof, and dataset. Activation
seals the Lease creation/expiry instants, exact quote line items, aggregate
committed cost, and the admitted stop/hard limits (¥1,350/¥1,500 by default)
into the root-only load environment.
Formal-chain checks the 81.5-hour admission reserve, then re-evaluates
conservative accrued host/retention/traffic cost and the one-hour expiry cleanup
reserve every five seconds through both phases. `chat-lifecycle-formal-finalize.yml` waits for
that process to exit, requires complete JSON/Markdown evidence pairs, and
uploads the terminal chain result or bounded
diagnostics. Both stage finalizers also capture a fixed-size terminal bundle of
four-host systemd state and journal windows, a fifteen-minute selected
Prometheus range plus target state, and bounded WuKongIM heap/goroutine profiles;
missing terminal evidence invalidates an otherwise passing result. It uses the
same bounded live-diagnosis and report-rescue policy as
rehearsal, and only then releases the exact Lease to zero inventory. Failed
Release attempts persist a stage-authenticated cleanup continuation for the
next scheduled pass; the generic Cloud Lease sweeper remains the independent
expiry backstop.

## Workflow maintenance

- Keep external Actions pinned by full commit SHA.
- Keep pull-request checkouts credential-free (`persist-credentials: false`).
- Put logic in scripts or Go commands; keep YAML to triggers, permissions and
  job wiring.
- Read this file before invoking or changing any Workflow.

Run:

```bash
GOWORK=off go test ./scripts/... -run Workflow -count=1
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.9 -shellcheck=
```
