# GitHub Actions tool catalog

This directory contains:

- `Agent Tool - ...`: a bounded, on-demand worker;
- `Safety Automation - ...`: an autonomous controller or safety backstop.

Use the stable filename when invoking a Workflow. Workflows that create cloud
resources, change permissions, or spend money still require explicit user
authorization and the applicable budget.

## Catalog

| File | Display name | Purpose |
| --- | --- | --- |
| `chat-lifecycle-rehearsal.yml` | `Agent Tool - Start Chat Lifecycle Rehearsal` | Builds, quotes, acquires, deploys, and hands a full-scale two-hour rehearsal to remote systemd |
| `chat-lifecycle-rehearsal-finalize.yml` | `Safety Automation - Finalize Chat Lifecycle Rehearsals` | While armed, uploads a terminal rehearsal report before Release and reconciles the Lease to zero inventory |
| `chat-lifecycle-formal.yml` | `Safety Automation - Start Fresh Formal Chat Lifecycle` | While armed, consumes an authenticated released rehearsal transition and starts a fresh 96-hour formal Lease |
| `chat-lifecycle-formal-finalize.yml` | `Safety Automation - Finalize Formal Chat Lifecycle Runs` | While armed, collects the same-Lease Soak/capacity/recovery result before Release and zero-inventory proof |
| `chat-lifecycle-stop.yml` | `Agent Tool - Stop Chat Lifecycle Request` | Seals one request-level stop marker and requests coordinated cancellation plus bounded operator-stop finalization |
| `three-node-chat-lifecycle-regression.yml` | `Safety Automation - Three-Node Chat Lifecycle Regression` | Enforces 400 ms p99 on focused 500 SEND/s PR benchmarks, runs a sealed 500 SEND/s three-node correctness/drain smoke, and runs one fresh nightly ten-minute 500 SEND/s regression directly without the diagnostic rate staircase; 1,000 SEND/s remains a dedicated capacity-environment gate |
| `review-agent-pr-signal.yml` | `Safety Automation - Review Agent PR Signal` | Emits a credential-free lifecycle or exact-command wake-up hint |
| `review-agent.yml` | `Safety Automation - Review Agent Controller` | Re-reads GitHub facts and signed state, then plans one lifecycle transition |
| `review-agent-run.yml` | `Agent Tool - Review Pull Request` | Runs one exact review or explanation generation |
| `issue-agent-pr-signal.yml` | `Safety Automation - Issue Agent PR Signal` | Emits credential-free lifecycle and Review hints for Issue Agent PRs |
| `issue-agent.yml` | `Safety Automation - GitHub Issue Agent` | Reconciles Issue work and Review Agent repair requests |
| `issue-agent-engineer.yml` | `Agent Tool - Issue Engineer` | Runs one exact Context Builder, Codex Engineer, and clean Verifier chain |
| `manager-browser-smoke.yml` | `Safety Automation - Manager Browser Smoke` | Builds the production Manager bundle and runs the desktop/mobile Chromium matrix against a real three-node cluster |
| `easysdk-release-acceptance.yml` | `Safety Automation - EasySDK Released Package Acceptance` | Resolves the pinned Android, iOS, and Flutter registry releases and proves bidirectional messaging against the current Product Gateway on hosted simulators/emulators |
| `native-package-preview.yml` | `Safety Automation - Validate Native Package Preview` | Builds unsigned amd64 deb/rpm previews and checks non-activating installs on the four supported Linux distributions |
| `cloud-lease-oidc-setup.yml` | `Agent Tool - Configure Cloud Lease OIDC Roles` | Reconciles and live-verifies the three workflow-conditioned Cloud Lease roles |
| `cloud-lease-provision.yml` | `Agent Tool - Provision Cloud Lease` | Quotes or explicitly acquires one generic Alibaba Cloud Lease |
| `cloud-lease-observe.yml` | `Agent Tool - Inspect Cloud Lease` | Reconstructs exact Lease inventory through the read-only Observer role |
| `cloud-lease-analyze.yml` | `Agent Tool - Analyze Chat Lifecycle Cloud Lease` | Authenticates one retained chat-lifecycle handoff, proves live Lease inventory, and brokers one bounded Analysis MCP session |
| `cloud-lease-release.yml` | `Safety Automation - Release Cloud Leases` | While armed, releases one exact Lease and runs the protected 15-minute expired/cleanup-pending repository sweep |
| `cloud-deployment-bundle.yml` | `Agent Tool - Build Cloud Deployment Bundle` | Builds and seals one procurement-independent offline Ubuntu four-host payload |
| `cloud-deployment-activate.yml` | `Agent Tool - Activate Cloud Deployment` | Installs and gates one exact offline bundle on an active four-host Lease |
| `cloud-sim-provision.yml` | `Agent Tool - Provision Cloud Simulation` | Creates a leased Alibaba Cloud Simulation Run |
| `cloud-sim-analyze.yml` | `Agent Tool - Analyze Cloud Simulation` | Operates one bounded cloud analysis session |
| `cloud-sim-oidc-subject.yml` | `Agent Tool - Configure Cloud Simulation OIDC Subject` | Configures and verifies the cloud OIDC subject |
| `cloud-sim-cleanup.yml` | `Safety Automation - Reconcile Cloud Simulation Resources` | Destroys expired cloud leases and supports exact cleanup |
| `cloud-sim-monitor.yml` | `Safety Automation - Patrol Cloud Simulation Runs` | Patrols retained live runs and records bounded health evidence |
| `docker-image-publish.yml` | `Safety Automation - Publish Docker Images` | Builds one immutable multi-platform GHCR image, mirrors its digest to Docker Hub and Alibaba Cloud, then advances eligible stable aliases |
| `docs-pages.yml` | `Safety Automation - Publish Documentation to GitHub Pages` | Verifies and deploys the exact documentation artifact, then optionally refreshes the pre-provisioned Alibaba Cloud CDN |
| `docs-cdn-certificate.yml` | `Safety Automation - Renew Documentation CDN Certificate` | While explicitly enabled, renews and deploys the public CDN certificate through ACME DNS-01 and Alibaba Cloud OIDC |
| `binary-release-publish.yml` | `Safety Automation - Publish WuKongIM Binaries` | Builds immutable Linux/macOS archives plus unsigned Linux amd64 DEB/RPM assets and publishes the exact set to the tag's GitHub Release |

## Documentation Pages

`docs-pages.yml` is the sole documentation publisher. A merge to `main` that
changes `docs-site/**` starts it automatically; `workflow_dispatch` is reserved
for first publication and recovery. The build job has read-only repository
access, installs the locked Bun dependencies, runs `bun run verify`, and uploads
only the resulting `docs-site/out` directory. The deploy job receives only the
Pages and OIDC permissions required by GitHub's deployment API.

The repository Pages source must remain `workflow`, and the unchanged
`github-pages` Environment is the content-publication boundary. In the planned
CDN topology, GitHub Pages owns `origin-docs.githubim.com` and remains the sole
content origin. Alibaba Cloud CDN serves the canonical public
`docs.githubim.com` domain and sends HTTPS origin requests with
`origin-docs.githubim.com` as the address, Host, and SNI. GitHub's repository
Pages Settings/API is the sole custom-domain authority. The export retains
`.nojekyll` but deliberately contains no `CNAME`.

After a successful Pages deployment, an isolated refresh job may assume the
refresh-only Alibaba Cloud role from the `docs-cdn` Environment. The job is
skipped unless repository Variable `DOCS_CDN_ENABLED` is exactly `true`. Once
enabled, a missing or invalid binding fails before OIDC exchange. It refreshes
a bounded set of stable public URLs; it does not create provider resources,
change DNS, replace CDN settings, or broadly purge content-addressed assets.

`docs-cdn-certificate.yml` is a separate certificate safety automation. Its
scheduled and manual paths are also inert while `DOCS_CDN_ENABLED` is disabled.
When enabled, it uses the `docs-cdn-certificate` Environment, a distinct
certificate-rotation OIDC role, and the Environment Secret
`DOCS_ACME_ACCOUNT_BUNDLE_B64` to renew the Let's Encrypt edge certificate for
`docs.githubim.com`. GitHub Pages independently owns and renews the origin
certificate for `origin-docs.githubim.com`; neither private key crosses that
boundary.

These workflows consume repository Variables `DOCS_CDN_DOMAIN`,
`DOCS_CDN_OIDC_PROVIDER_ARN`, `DOCS_CDN_OIDC_AUDIENCE`,
`DOCS_CDN_REFRESH_ROLE_ARN`, `DOCS_CDN_CERTIFICATE_ROLE_ARN`, and
`DOCS_ACME_EMAIL`. Their presence is not evidence that DNS, CDN, RAM, delegated
ACME validation, or GitHub Pages custom-domain setup is complete. Those are
administrator-owned external operations and must remain disabled until the
cutover prerequisites and rollback snapshot in the
[documentation CDN runbook](../../docs/superpowers/runbooks/docs-alibaba-cdn.md)
have been verified.

## Native package preview

`native-package-preview.yml` is a credential-free validation workflow, not a
publisher. It builds unsigned Linux amd64 `.deb` and `.rpm` files from
`.goreleaser.packages.yaml`. After the build, an integration test creates
one-run, one-day `TEST ONLY` APT and RPM signing keys outside the repository,
signs temporary repositories, and verifies both signatures and the exact APT
`Release -> Packages -> .deb` and RPM `repomd.xml -> primary metadata -> .rpm`
closures in clean containers. The temporary keys and signed repositories are
never uploaded or published.

The workflow also inspects package payloads and installs, reinstalls through
the package-manager upgrade path, and removes them inside Ubuntu 24.04, Debian
12, Rocky Linux 9, and AlmaLinux 9 containers. The package creates the
`wukongim` service account and persistent directories but does not create
`/etc/wukongim/wukongim.toml`, enable the unit, start it, or restart it during
upgrade. The upgrade check records every `systemctl` request and verifies that
the operator-owned configuration and data remain byte-identical. Only the
unsigned preview packages and checksums are retained as Actions artifacts for
14 days; this workflow still publishes no APT/YUM repository.

The separate public `WuKongIM/packages` repository owns the fail-closed GitHub
Pages bootstrap at the verified HTTPS endpoint `packages.githubim.com`; it
currently publishes only a `signing_not_provisioned` status and no package
indexes. Both repositories enforce immutable Releases, and the custom-domain
DNS and certificate are provisioned. Reviewed public fingerprints, signing
custody, cross-repository dispatch, and the production publisher remain
intentionally outside this credential-free workflow. Exact unsigned source
package assets are supplied by the tag-bound binary Release described below,
but they must not enter package indexes until the remaining production controls
are provisioned and reviewed.

## Docker image publishing

`docker-image-publish.yml` is the sole official container-image publisher. A
new `v*` tag starts an automatic release, while `workflow_dispatch` accepts one
existing tag for a first publication or incomplete-release recovery. The tag
must be strict SemVer without `+build` metadata, resolve to a commit reachable
from `origin/main`, and remain the immutable source identity. Before checking
credentials or publishing an image, the Workflow validates that the tagged
`CHANGELOG.md` contains one non-empty, correctly formatted section for the
exact tag. It never derives release notes from commits or GitHub-generated
notes.

GHCR is the canonical build target. The Workflow builds
`linux/amd64,linux/arm64` security candidates and blocks publication when
Trivy reports any Critical or High vulnerability. A recovery run applies the
same per-platform scan to the existing canonical digest before it can fill a
missing mirror. After the scan passes, the Workflow builds the canonical
multi-platform image, creates signed GitHub build provenance plus a CycloneDX
SBOM attestation for each platform, and copies the exact runtime manifest
digest to all three public repositories:

- `ghcr.io/wukongim/wukongim`;
- `docker.io/wukongim/wukongim`;
- `registry.cn-shanghai.aliyuncs.com/wukongim/wukongim`.

The exact Docker tag removes the Git `v` prefix. A stable `v3.1.2` may advance
`3.1`, `3`, and `latest` only when it is the highest stable repository SemVer
in each corresponding range. Pre-release tags, including date suffixes such as
`v3.1.2-20260831`, publish only their exact tag. Exact tags are never
overwritten. A manual recovery reuses an existing canonical GHCR digest, fills
only absent exact mirrors, verifies identical digests and the two required
platforms, and advances aliases only after that verification. BuildKit's inline
provenance and SBOM manifests stay disabled because Alibaba Cloud Registry
rejects their OCI empty-config media type; the detached signed GitHub
attestations preserve this evidence without changing the portable runtime
digest. Every successful run uploads a 90-day JSON release receipt and both
per-platform SBOMs, and writes the same source, builder, digest, platform, and
image facts to the Job Summary.

Repository administrators must pre-create all three repositories with public
pull visibility and configure a `docker-publish` Environment. Limit that
Environment to protected `main` for manual recovery and trusted version tags
for automatic publication, do not require a reviewer, and configure:

- Variable `DOCKERHUB_USERNAME`;
- Secret `DOCKERHUB_TOKEN`;
- Variable `ALIYUNHUB_USERNAME`;
- Secret `ALIYUNHUB_TOKEN`.

GHCR uses the job-scoped `GITHUB_TOKEN`; no standing GHCR credential is
required. Missing credentials, a conflicting exact tag, a registry copy
failure, a digest mismatch, or a missing platform fails the complete release.

## Binary release publishing

`binary-release-publish.yml` publishes downloadable WuKongIM executables from
the same strict SemVer `v*` source identity used by the container publisher. A
new tag starts the workflow automatically; the manual `version` input accepts
one existing tag for first publication or incomplete-draft recovery. A manual
run must itself be dispatched from that exact tag, for example
`gh workflow run binary-release-publish.yml --repo WuKongIM/WuKongIM --ref "$tag" -f version="$tag"`;
a run dispatched from `main` fails before building because GitHub provenance
inherits the run ref and Workflow commit. The tag must contain this Workflow,
resolve to a commit reachable from `origin/main`, and may not contain SemVer
build metadata. A historical tag without the Workflow needs a new SemVer tag
rather than provenance synthesized from current `main`.

Each run builds deterministic `.tar.gz` archives for `linux/amd64`,
`linux/arm64`, `darwin/amd64`, and `darwin/arm64`. Windows is not advertised
because the server currently contains Unix-only runtime and filesystem paths.
Every archive includes the executable, English and Chinese readmes, and
`wukongim.toml.example`. When the tagged source contains
`.goreleaser.packages.yaml`, the same run also uses the commit-pinned
GoReleaser action with GoReleaser `v2.18.0` to build unsigned Linux amd64
packages, normalized to exactly:

- `wukongim_<version>_linux_amd64.deb`;
- `wukongim_<version>_linux_amd64.rpm`.

All archives and native packages share one SHA-256 checksum file and one signed
GitHub build-provenance statement. The workflow verifies Linux binaries under
amd64 and arm64 and retains the complete asset set plus a JSON receipt as
Actions evidence for 90 days. Native packages remain unsigned source assets;
the separate package repository must apply and verify its production package
and repository signatures before publication. Tags containing the version
command also prove the embedded version, full source commit, and `release`
build source. Older tags that predate `.goreleaser.packages.yaml` retain their
original five-asset recovery contract and remain bound by the immutable tag,
checksums, and attestation.

The workflow creates a draft Release without publishing it, uploads every
expected archive, native package, and checksum, downloads and byte-compares the
complete exact asset set, and only then publishes the verified draft once.
Recovery may compare existing
assets and fill missing files only in that same draft. Draft discovery scans
all Releases for one exact tag match, then binds creation, upload, verification,
and publication to the resulting numeric Release ID; it never relies on the
published-by-tag endpoint to find a draft. Asset upload accepts only the exact
numeric-ID `uploads.github.com` URL returned by GitHub and checks each upload
response against the local size and SHA-256. Immediately before publication it
rechecks that the remote tag still peels to the validated source commit, that
repository-level immutable Releases remain enabled, and that the draft still
has the exact asset names, sizes, and digests verified earlier. Any existing
published Release fails closed: a complete one is already final, while an
incomplete one must never be reused and requires a new SemVer tag. A
pre-release SemVer tag must correspond to a GitHub pre-release; stable tags
must correspond to a normal Release. After publication, the workflow verifies
that GitHub sealed the same numeric Release ID as immutable, checks every
asset's API size and any available SHA-256 digest against the local file, and
always performs an ID-bound download and byte comparison. The same ID-bound
byte comparison is repeated immediately before publication.

The human-authored part of every new Release comes only from the exact tagged
section in the root `CHANGELOG.md`. The file keeps `Unreleased` first and uses
`## [vMAJOR.MINOR.PATCH[-PRERELEASE]] - YYYY-MM-DD` version boundaries with the
fixed bilingual categories documented in that file. The Workflow rejects a
missing, duplicate, empty, or malformed target section before building. It
preserves the extracted Markdown verbatim and appends only machine-derived
source, binary, checksum, image, digest, platform, Docker Workflow, and compare
facts. It never falls back to GitHub-generated notes, a commit log, or an empty
body. Every discovered, created, pre-publication, and published numeric Release
ID must carry the exact rendered body.

For manual recovery, the release-note extractor may come only from the exact
`github.workflow_sha` commit, which the identity gate requires to resolve to
the tagged source. It can never be borrowed from current `main`. A historical
tag missing either this Workflow or the extractor needs a new SemVer tag;
automatic tag publication likewise fails when the tagged source omits the
extractor.

The binary publisher waits for a successful Docker publisher run for the same
tag, then verifies that GHCR, Docker Hub, and Alibaba Cloud expose one identical
`linux/amd64,linux/arm64` digest whose per-platform labels match the exact tag
source. A Docker failure or bounded wait timeout leaves any matching draft
unpublished. After repair, manual dispatch resumes only when the draft body and
assets still exactly match the tagged source. Pre-release Changelog entries are
incremental from the previous pre-release in the same version line; stable
entries summarize user-visible changes since the previous stable release.

Repository administrators must configure a `binary-publish` Environment,
allow only trusted version tags for both automatic publication and manual
recovery, and avoid a reviewer gate for tag-triggered runs. The workflow uses
only its job-scoped `GITHUB_TOKEN`; it requires no standing release credential.
Until those Environment deployment restrictions have been configured and
verified remotely, administrators must not push a release tag or manually
dispatch this workflow.

Immediately before pushing a release tag or starting manual recovery, an
administrator must also verify that repository-level immutable Releases remain
enabled in repository settings or through the administrator-only
`GET /repos/{owner}/{repo}/immutable-releases` endpoint. The Actions
`GITHUB_TOKEN` cannot read that administration endpoint, so the Workflow does
not request or store an administrator token. It instead verifies after
publication that GitHub sealed the exact numeric Release as immutable.

For a user-visible pull request, add the release note under `Unreleased` when
the change is made. A PR with no user-visible behavior must explain the reason
in the repository PR template and receive the maintainer-applied
`skip-changelog` label. The existing Review Agent checks the repository rule;
the tag Workflows remain the final fail-closed publication gate. Before tagging,
run:

```bash
awk -v version=v3.0.0-beta.5 \
  -f scripts/extract-release-notes.awk CHANGELOG.md >/dev/null
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

## Review Agent

Every pull request defaults to human handling. A model review starts only after
a repository administrator posts the exact `@review-agent review` command:

```text
administrator @review-agent review
  -> zero-permission Signal
  -> protected-default-branch Controller
  -> fresh GitHub facts + signed PR state + signed scheduler
  -> exact context + deterministic checks + one ephemeral model session
  -> evidence validation + signed terminal state
  -> status comment + formal Review + Review Agent Verdict
  -> exact-head auto-merge for a repository admin or organization member
     | otherwise wait for a human merge
```

Other PR and Review events may close state, cancel stale work, or repair a
projection, but cannot create a review generation. A new commit invalidates
and cancels any old work; an administrator must explicitly review the new head.

The Signal is a hint, not authority. It has no token permission, Secret,
checkout, Artifact, cache, network command, or candidate execution. The
Controller always re-reads the pull request and never checks out candidate
code.

`review-agent-run.yml` separates these authorities:

- candidate checkout and trusted checks use credential-free jobs;
- `review-agent-model` exposes only `OPENAI_API_KEY`;
- `review-agent-state-writer` exposes only the State Writer App key;
- `review-agent-publisher` exposes only the Review Agent App key;
- the isolated dispatcher has only `actions: write`.

The Worker validates and checks out the exact protected control revision once,
builds `wkreviewagent`, `wkreviewcheck`, and `wkreviewcheckmcp` in one job, and
publishes one run-scoped Artifact. Every consuming job verifies the embedded
control SHA and complete SHA-256 manifest, installs only its allowlisted
binaries, and removes the downloaded bundle before continuing. Candidate code
cannot build, replace, or upload this bundle, and no shared build cache crosses
Worker runs.

The Controller also compiles `wkreviewagent` only once per run. When a plan
requires a state write or projection, credentialed jobs consume a run-scoped,
control-SHA-bound manifest Artifact instead of rebuilding it. A true no-op
uploads only its bounded plan; State Writer, Publisher, and Dispatcher jobs are
skipped.

The model can review and invoke only protected named checks. It cannot edit the
PR, commit, push, merge, resolve threads, dismiss Reviews, or publish its own
verdict. A trusted validator maps signed state to the sole required Check
`Review Agent Verdict`. Only `approved` maps to success. `changes_required`
maps to failure, and `inconclusive` maps to `action_required`. A human
`REQUEST_CHANGES` Review remains independently blocking.

Repository Skills use the protected `agent-artifact-contracts` and
`skill-focused-contracts` checks. Their Review Agent command definitions live
only in `.github/review-agent/policy.json`; focused Skill test commands live
only in `.agents/skill-tests.json`. The structural check never discovers or
executes files from a Skill's `scripts/` directory.

FLOW navigation uses the protected `flow-doc-contracts` check. Its command and
path selection live only in `.github/review-agent/policy.json`; it enforces the
strict schema, 150-line limit, local navigation references, and canonical
generated index.

The model request passes through one root-owned loopback proxy that clamps
`max_output_tokens` to the protected policy and injects the OpenRouter
credential. The root-only credential handoff file is deleted before the
listener is published. Codex and candidate checks cannot read the credential,
replace the proxy, or reach an unclamped transport path.

After publishing an `approved` verdict, the protected Publisher re-reads the
exact PR head, mergeability, human Reviews, author association, and author
repository permission. It merges only when the author is an organization
`MEMBER`/`OWNER` or currently has repository `admin` permission. Every other
approved PR remains open and is marked as requiring a human merge. The merge
request is fenced to the reviewed head SHA and still obeys repository rules.
Repository administrators retain GitHub's manual merge authority for every PR
whether or not Review Agent was invoked or produced a verdict.

Each review infrastructure attempt has a signed 90-minute deadline.
Infrastructure failure is retried once inside the same generation with a fresh
signed attempt deadline, so the complete generation remains bounded to at most
180 minutes. A result that is late for its active attempt is forced to
`inconclusive`. A merge conflict bypasses the model and publishes
`changes_required`. Candidate baseline commands run only after the shared
network fence disables both Docker access and `sudo`. Candidate checks receive
isolated loopback inside a rootless network namespace whose host loopback is
disabled. The trusted baseline host keeps runner transport available only so
pinned post-job Actions can upload evidence; candidate code never runs there.
The model host keeps GitHub runner transport intact. The pinned Codex Action
installs the exact CLI, then the Workflow invokes
`codex exec --dangerously-bypass-approvals-and-sandbox` under model-only CPU,
address-space, and process limits. This gives Codex full runner-user filesystem
and public-network access without its internal Bubblewrap sandbox. The model
receives no GitHub or App credential, inherits no host environment, and starts
only after Docker and `sudo` are disabled. Candidate check commands still run
inside the rootless network namespace and their Bubblewrap sandboxes. The
trusted validator rejects any tracked candidate-tree mutation.

Ubuntu AppArmor may restrict unprivileged user namespaces on hosted runners.
Each candidate runner installs one root-owned Review Agent `unshare` copy and
loads a path-specific profile granting only `userns`; the global restriction
is never disabled. After the namespace and its network rules are ready, the
job unloads the temporary profile and removes both the copied binary and its
directory before any candidate command can run. Private-CIDR, quota, and
connection fences live inside the candidate namespace; Docker and `sudo` are
disabled on the trusted baseline host without blocking its Artifact transport.
Explanation-only sessions do not install that profile or create a candidate
network namespace because they never execute candidate checks. The Check MCP
is a required Codex dependency. Its credential-free stdio server completes the
Codex handshake on the trusted model host, while each resolved protected check
enters the pre-built private-network namespace before its disposable checkout
and bubblewrap sandbox start.
Failure to initialize the MCP stops the model session instead of silently
removing the protected check tools.

The exclusive documentation fast path covers `docs/`, `docs-site/`,
`README.md`, and `README_CN.md`. It runs `docs-contracts` without falling
through to repository-default Go checks. That protected check includes the
documentation source contracts, the complete `docs-site` verification and
static export, broken-link and metadata gates, and the buildable JavaScript Web
sample. Changes to the sample, its published SDK/API contract pages and machine
artifacts, shared developer-page presentation sources, the Product
HTTP/use-case/cluster-read path used by it, Gateway authentication or
WKProto/WebSocket transport, the E2E harness, or the wire ReasonCode enum also
run the protected `docs-integration` check against a real single-node cluster
and Chromium. Any mixed or non-allowlisted change still receives the union of
its applicable checks.

The `docs-integration` check pins Bun 1.3.11 and Node.js 22.12.0. It installs
the frozen docs tree and locked JavaScript sample before any typechecked build,
reruns the source-alignment contract tests, and completes an initial unverified
build/output gate before the browser smoke, so the same static export is
available to the accessibility checks. It then installs locked Chromium, runs
the focused real-process E2E against a 256-Hash-Slot single-node cluster, and
accepts a verification receipt only from a clean committed HEAD. The helper
strictly validates the bounded receipt against the current source revision,
sample lock hash, SDK, Node.js, Playwright, and Chromium identities before a
second verified build and output gate. Its unique receipt directory is always
removed; a failed browser run may retain only three redacted PNG screenshots,
each no larger than 2 MiB, under the ignored `tmp/docs-site-e2e/` directory.

Worker dispatch is serialized per pull request. The exact run title derived
from pull request, signed lease, and infrastructure attempt is the idempotency
key at both Controller and retry-drain boundaries, so concurrent recovery
cannot start the same attempt twice.

Review-request and interaction budgets are signed per head SHA. An
authorized reconsideration of the current head binds a new generation from
fresh eligible facts even when the protected control revision, intent, base,
or test-merge revision changed; it consumes reconsideration allowance.

Missing Context, reviewer, or trusted-baseline artifacts are evidence of an
infrastructure failure, not reasons to abort the state machine. The Evidence
job records the bounded retry or terminal `inconclusive` completion so signed
state and the repository queue always advance.
Before validation, that job normalizes the bounded model output through the
Go ReviewResult decoder. Shell code does not implement a second JSON-shape
policy.

There is no scheduled Review Agent scan. A failed Controller effect is retried
once; other recovery comes from a later event or an exact manual Controller
dispatch.

See [`docs/agents/review-agent.md`](../../docs/agents/review-agent.md) for the
state model, commands, security boundaries, and repository setup.

## Issue Agent integration

The Issue Agent remains an engineering agent and still cannot merge. When the
latest exact-head formal Review from the configured Review Agent Bot is
`CHANGES_REQUESTED`, only its unresolved findings may authorize one of the
Issue Agent's bounded repair loops. Human Review comments remain human
authority and never masquerade as an automated repair command.
Its reusable workflow ends after the clean Verifier; the caller-owned
Publisher separately finalizes every accepted task from the protected
`issue-agent-publisher` Environment, including failed engineering attempts.
For a Ready Agent PR whose exact App-owned candidate is behind `main`, that
same Publisher may mechanically recreate the candidate on current `main` only
when none of its paths changed. The expected-head ref swap advances signed
state, consumes a bounded sync attempt, invalidates prior Review authority, and
requires a fresh `Review Agent Verdict`; overlaps and external heads fail
closed without asking Codex to merge them.

See [`docs/agents/issue-agent.md`](../../docs/agents/issue-agent.md).

## Cloud Simulation

Cloud creation and permission changes remain explicit Agent Tools. Cleanup and
live-run patrol are the only scheduled safety automations while provider
inventory exists. Provision re-enables both after persisting its provider
binding and before creating billable resources. A complete scheduled cleanup
with no retained Run and no reconciliation failure disables patrol first and
cleanup last, so an idle repository consumes no recurring Runner capacity.
Provider credentials, analysis credentials, and cleanup authority stay in
their documented separate Environments.

See
[`docs/superpowers/runbooks/cloud-simulation.md`](../../docs/superpowers/runbooks/cloud-simulation.md).

## Cloud Lease identity and lifecycle

The generic Cloud Lease flow is distinct from the older Cloud Simulation
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
identity/deployment credentials are configured. Its complete operator surface
is exactly `source_sha`, fixed operator `tangtaoit`, one request-scoped Codex
diagnostic Ed25519 public key, and `request_id`; infrastructure, budget,
duration, workload, and retry values come only from the reviewed repository Run
Plan. It builds the immutable bundle, obtains a read-only Quote, acquires one
12-hour AutoRelease Lease by consuming that exact admitted Quote, activates the deployment, and starts the dormant rehearsal
unit. The runner exits after the remote `run-start.json` proves 10,000 full
syncs and acceptance of the first full 2,000 SEND/s grant. The remote rehearsal
uses the same sealed five-second accrued-cost and one-hour expiry-reserve guard
as formal execution, with a two-hour active-duration admission requirement.
Before paid Acquire, it seals the generated Deployment/monitor private key with
the Lease ID, Plan digest, source SHA, and expiry already fixed by the Run Plan.
The acquired Receipt must equal that identity before Deployment dispatch.

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
2,000-SEND/s recovery in one `wkbench formal-chain` process with the same
worker fence, generation, observer, lifecycle proof, and dataset. Activation
seals the Lease creation/expiry instants, exact quote line items, aggregate
committed cost, and ¥1,350/¥1,500 limits into the root-only load environment.
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
- Keep candidate checkouts credential-free and reject tracked-tree mutation.
- Never expose App keys to candidate or model jobs.
- Update policy, schemas, Workflows, docs, and their contract tests together.
- Read this file before invoking or changing any Workflow.

Run:

```bash
GOWORK=off go test ./scripts/... -run 'Workflow|ReviewAgent' -count=1
node --test .github/review-agent/responses-budget-proxy.test.mjs
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.9 \
  .github/workflows/*.yml
```
