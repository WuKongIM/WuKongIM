# Cloud Simulation Operations Runbook

This runbook operates the Alibaba-first cloud simulation system. It creates no
OSS bucket, image registry, or historical Evidence Bundle. AccessKey mode reads
an operator-created RAM AccessKey only from GitHub Repository Secrets; OIDC
mode stores no cloud AccessKey. A released run is no longer analyzable.

## 1. Choose cloud authentication

### Simplest setup: GitHub AccessKey Secrets

Create an AccessKey for a dedicated Alibaba RAM user with the Cloud Simulation
provisioner permissions. Do not use an Alibaba root-account AccessKey unless
there is no safer temporary canary option. In the GitHub repository, open
`Settings -> Secrets and variables -> Actions` and create exactly these two
Repository Secrets:

```text
ALIBABA_CLOUD_ACCESS_KEY_ID
ALIBABA_CLOUD_ACCESS_KEY_SECRET
```

Both values must exist together. A partial pair fails before the workflow calls
Alibaba. Never paste either value into a workflow input, source file, issue,
Artifact, or Codex conversation.

No CloudShell, OIDC Provider, RAM Role, provider-config Variable, cloud account
number, or OpenAI API key is required in this mode. Provision defaults to
`cn-hangzhou` and the current remote `main`; both remain explicit Action inputs
when an override is needed. Before creating any billable resource it discovers
the authenticated account hash, an ESSD-capable zone, the newest audited x86
Alibaba Cloud Linux 3 image, and live candidates for all three infrastructure
presets. The resulting non-secret `provider.json` is retained for 90 days in a
Run-Identity-scoped Artifact so Analysis and Cleanup use the same account and
region binding.

Keep both Secrets until Cleanup proves the run is `released` with zero residual
resources. Deleting them earlier prevents AccessKey-mode scheduled cleanup from
authenticating. Rotate or delete the RAM AccessKey after the final real-cloud
drill if this repository will not keep using AccessKey mode.

### Hardened alternative: one-time CloudShell OIDC bootstrap

CloudShell is used only to establish the first trust edge. The browser session
already has an Alibaba identity, so the repository CLI can use the default
credential chain without printing or exporting an AccessKey. Ordinary GitHub
workflows cannot create their own OIDC trust before this step exists.

#### Recommended one-command setup

Push the Cloud Simulation workflows to the repository's remote `main`, open
Alibaba CloudShell, and run:

```bash
git clone https://github.com/WuKongIM/WuKongIM.git
cd WuKongIM
./scripts/cloud-sim/setup.sh
```

The wizard uses the current CloudShell identity and a one-time GitHub browser
login. It recommends the current CloudShell profile region from the live ECS
region list, then asks only for one confirmation of the
non-billable RAM/OIDC plan. It:

- discovers the current Alibaba account without copying it to GitHub;
- derives repository-scoped OIDC Provider and RAM Role names so two repositories
  in one Alibaba account cannot overwrite each other's trust;
- selects a zone with ESSD support, the latest standard x86 Alibaba Cloud Linux
  3 image in the audited image family, and up to three currently available,
  paginated x86 non-GPU spot candidates for each `small`, `standard`, and
  `stress` capacity class after checking every compatible candidate until the
  class has three choices or the inventory is exhausted;
- installs checksum-pinned Go and GitHub CLI binaries in the user's cache when
  those commands are absent or do not exactly match the repository pins;
  downloads are IPv4/HTTP 1.1, low-speed and
  time bounded, resumable across reruns, and retained under
  `${XDG_CACHE_HOME:-$HOME/.cache}/wukongim-cloud-sim/downloads/`; missing Go
  prefers the Alibaba Golang mirror before the official fallback, while GitHub
  CLI remains pinned to its official GitHub Release;
- applies and re-plans the existing `wkcloudbootstrap` authority;
- creates missing GitHub Environments without overwriting protection rules on
  existing Environments;
- sets and reads back repository and Environment Variables, configures the
  exact OIDC subject, then dispatches and waits for a workflow correlated by a
  unique setup identifier that proves GitHub OIDC can exchange for short-lived
  Alibaba credentials.

No ECS instance, disk, EIP, security group, vSwitch, or VPC is created. The
wizard saves its non-secret removal configuration with mode `0600` under
`${XDG_CONFIG_HOME:-$HOME/.config}/wukongim/cloud-sim/<owner_repo>/` and prints
the exact Provision Workflow URL and recommended first-canary inputs. It is
safe to rerun after a partial failure.

Use `--region`, `--repository`, or `--yes` only when their values are already
reviewed. No OpenAI API key is requested or stored. Local analysis uses the
Codex CLI authenticated with the operator's ChatGPT subscription. The selected
SKU list is a setup recommendation, not a capacity promise: Provision still
performs the authoritative live price, capacity, quota, and hard-cost checks
before creating resources.

If the GitHub Release CDN is unreachable even after the bounded retries,
download the exact printed `gh_<version>_linux_<arch>.tar.gz` URL on another
machine, upload that unchanged file with the CloudShell upload button, move it
to the printed `.../wukongim-cloud-sim/downloads/` cache path, and rerun the
same setup command. The pinned SHA-256 is still verified before extraction.

#### Manual fallback

From Alibaba CloudShell:

```bash
git clone https://github.com/WuKongIM/WuKongIM.git
cd WuKongIM
git checkout main
cp .github/cloud-sim/alibaba-bootstrap.example.json bootstrap.json
${EDITOR:-vi} bootstrap.json
```

Replace the account, region/zone, audited image, and audited spot SKU values.
The CLI resolves GitHub's current OIDC root fingerprint through a
system-trusted TLS connection when `oidc_fingerprints` is omitted. To make a
review fully reproducible, the resolved fingerprints may instead be recorded
explicitly in the bootstrap object.

Review, apply, and re-check idempotence:

```bash
GOWORK=off go run ./cmd/wkcloudbootstrap --config bootstrap.json plan
GOWORK=off go run ./cmd/wkcloudbootstrap --config bootstrap.json apply | tee bootstrap-result.json
GOWORK=off go run ./cmd/wkcloudbootstrap --config bootstrap.json plan
```

The second plan must have no changes. `apply` creates only one RAM OIDC
provider, two workflow-conditioned roles, and their two policies. It creates no
VPC, ECS instance, disk, EIP, or security group. The Provisioner role has two
independent trust statements: `cloud-sim-provision.yml` on `main` in the
`cloud-sim-provision` Environment, and `cloud-sim-cleanup.yml` on `main` in the
`cloud-sim-cleanup` Environment. The Analyzer trust accepts only
`cloud-sim-analyze.yml` and the one-time `cloud-sim-oidc-subject.yml`
connectivity check on `main` in `cloud-sim-analysis`. Configure required
reviewers on billable creation if desired, but never put a required reviewer on
`cloud-sim-cleanup`; its 15-minute lease reconciliation must remain unattended.

Alibaba RAM accepts only the OIDC `iss`, `aud`, and `sub` condition keys. After
the manual RAM apply succeeds, manually dispatch `Cloud Simulation - Configure
OIDC Subject` once from `main` and wait for both jobs to pass. The one-command
setup performs the same API mutation, dispatch, and live identity exchange
automatically. Both paths configure the repository
subject as `repo + context + job_workflow_ref`, allowing each RAM `oidc:sub` to
bind the exact repository, Environment, workflow file, and main branch. Do not
run Provision, Analyze, or Cleanup until one of these verification paths is
green.

Set these non-secret repository or Environment Variables from the output:

- `ALIBABA_CLOUD_SIM_OIDC_PROVIDER_ARN`
- `ALIBABA_CLOUD_SIM_PROVISIONER_ROLE_ARN`
- `ALIBABA_CLOUD_SIM_ANALYZER_ROLE_ARN`
- `ALIBABA_CLOUD_SIM_OIDC_AUDIENCE`
- `ALIBABA_CLOUD_SIM_CONFIG_JSON`

Before storing `ALIBABA_CLOUD_SIM_CONFIG_JSON`, replace its
`account_id_hash` with `account_id_hash` from `bootstrap-result.json`. Keep the
cloud account number itself out of GitHub configuration. Do not store an
OpenAI API key, Codex `auth.json`, or ChatGPT session in GitHub. The analysis
Environment contains only cloud federation configuration; local Codex
authentication never leaves the operator's device.

Bootstrap removal is protected:

```bash
GOWORK=off go run ./cmd/wkcloudbootstrap --config bootstrap.json remove
```

For one-command setup, use the saved `bootstrap.json` path printed by the
wizard instead of the manual path above.

Removal refuses while any tagged Simulation Run is active. Run it only when
the Cleanup Workflow proves no remaining run inventory.

## 2. Provision a run

Dispatch `Cloud Simulation - Provision` from `main`. The source SHA must be a
40-character commit reachable from `origin/main` when explicitly supplied; an
empty value selects the current remote `main`. Select a reviewed
`cloud-small`, `cloud-medium`, or `cloud-large` scenario, a compatible
`small`, `medium`, or `large` infrastructure preset, `30m`, `2h`, `24h`,
`48h`, or `168h` active duration, `2h` or `6h` analysis grace, and a hard CNY
cost ceiling. The seven-day large profile also requires its explicit cost
confirmation input.

For a quick end-to-end validation, select `cloud-small`, `30m` active duration,
and `2h` Analysis Grace. The workflow active-duration default is `48h`, while
the formal Alibaba canary remains `small + 2h`; the short validation does not
replace that attestation.

The build job has no cloud identity. The protected provision job uses the
complete AccessKey Secret pair when configured, otherwise it obtains a
short-lived OIDC credential. It quotes live price/capacity/quota, creates
exactly four spot hosts and their run network, transfers one sealed bundle
through a temporary simulator-only SSH `/32`, and starts
`wkbench-run.service` only after the complete three-node/256-slot gate passes.
The run generates separate
Manager diagnostic, Manager JWT, Bench API, and Worker Control capabilities;
the Bench capability is required on every node `/bench/v1/*` request. Keep the
Run Identity printed in the summary; there is no `latest` alias.

The workflow persists `ready` only after the full Bootstrap Gate, then persists
`running` with the exact active workload deadline after systemd accepts the
non-restarting wkbench unit. Provider reconciliation reports
`analysis_grace` after that deadline. If provisioning fails, the workflow keeps
the run only when the recorded Analysis MCP self-check is usable; otherwise it
immediately invokes full provider cleanup.

If provisioning is cancelled, native ECS auto-release still bounds every
compute host. The scheduled Cleanup Workflow reconciles disks, EIP, security
group, vSwitch, and VPC from mandatory tags every 15 minutes.
It reads Artifact metadata newest-first and downloads at most one provider
configuration per account/region binding, so retained 90-day run history does
not multiply cloud configuration downloads on every sweep.
Unbound provider-config Artifact names are ignored; runs from before this
Artifact format use the validated `ALIBABA_CLOUD_SIM_CONFIG_JSON` fallback.
Exact cleanup validates that fallback against the unique retained Run Locator's
account hash and region, and fails closed when the Locator cannot prove them.

After workload start, Provision uploads a minimal Finalization Schedule with
the Run Identity, workload deadline, initial analysis time, and lease expiry.
It contains no logs, metrics, profiles, diagnosis, or credential. If this
upload fails after `running` is persisted, the workload stays `running` until
provider reconciliation observes its deadline; workflow failure cannot shorten
the active workload window.

## 3. Analyze an exact live run

Install GitHub CLI and Codex CLI once. Sign in to GitHub, then sign in to Codex
with the same ChatGPT account that has the Pro subscription:

```bash
gh auth login
codex login
```

Then run analysis from a local checkout whose remote points to this repository:

```bash
./scripts/cloud-sim/analyze.sh <run_id>
```

For normal operation, prefer the single finalization command instead:

```bash
./scripts/cloud-sim/finalize.sh <run_id>
```

Keep the command running. It reads the exact Finalization Schedule, waits until
wkbench should have written its terminal summary, and runs an Analysis Run. If
the validated result still reports `workload_inspect.state=in_progress`, it
retains the run and retries while enough lease remains for another safe
session. It then dispatches exact Cleanup and repeats the provider-backed
released preflight using a structured released outcome rather than matching
console text.
It succeeds only after the exact Run reports empty provider inventory. If
analysis fails, cleanup still runs to stop billing and the command returns the
analysis failure after zero-resource verification. Finalization never starts
optional remediation, pushes a branch, or waits for CI before exact Cleanup.
It still accepts `--allow-fix-pr` as a compatibility flag, but defers that work
until a separate, explicitly requested post-cleanup repository operation.
Exact Cleanup request correlation comes from the finalizer's private `mktemp`
directory and does not depend on the OpenSSL CLI, so a missing optional crypto
tool cannot block the billing-stop dispatch. The analysis attempt owns a
lease-aware wall-clock deadline with cleanup reserve. Finalize arms exact
cleanup immediately after validating the checkout identity and creating its
private state directory. `INT`, `TERM`, or terminal disconnect (`HUP`) during
bounded GitHub authentication, workflow checks, schedule download, the
analysis-ready wait, or terminal analysis stops the owned process group and
then proceeds to exact Cleanup. Once cleanup starts, repeated terminal signals
are ignored by both Finalize and its bounded command wrappers; the workflow
join and independent released-state proof keep their whole-operation deadlines
as the local backstop.

The local command first dispatches a request-correlated, read-only `inspect`
operation to `Cloud Simulation - Analysis Session`. The workflow resolves the
unique 90-day Run Locator and compares it to current Alibaba inventory. Before empty inventory
can mean `released`, STS verifies the current Alibaba caller's account hash
and the adapter verifies the exact region against the locator; stale or
cross-account configuration fails closed.

- A missing or ambiguous locator reports `unknown_run`.
- A valid locator plus empty provider inventory reports `released`, prints
  `Simulation Run <run_id> 已由云厂商确认自动销毁，当前没有可分析的实时数据；分析已终止。`, and stops before Codex.
  The atomic result carries `provider.state=released` and
  `provider.resources=[]`. This terminal verification resolves only the pinned
  GitHub CLI; it does not require a local Go toolchain, OpenSSL/Base64 tools, or
  a reachable public-IP echo service.
- Existing resources with an unreachable MCP report `insufficient_evidence`,
  never `released`.

Only after `inspect` proves the exact run is live does the local command create
an ephemeral RSA key, resolve its direct public IPv4, and dispatch `prepare`
with strict client material under the same request correlation. Missing or
invalid client IPv4/key material fails closed before ingress opens. For a live
run, the workflow briefly admits its runner `/32`, pins the
run-specific CA fingerprint from protected resource tags, verifies the public
IP SAN, and exchanges its exact GitHub OIDC identity for a non-renewable
Analysis Token. It then moves the Analysis Access Window to the local client's
public `/32` and uploads only a request-correlated handoff containing metadata,
the pinned public CA, and the token encrypted to an ephemeral 3072-bit RSA
public key. The matching private key exists only in the local process. The
artifact expires after one day, while the token itself expires within 45
minutes and cannot be renewed.

The local command verifies the handoff identity and certificate fingerprint,
checks out the exact deployed source SHA in a detached temporary worktree,
decrypts the token, and starts an ephemeral read-only Codex session. Codex CLI
version and login probes have wall-clock bounds. Source checkout first verifies
the commit in the local object database; only a missing commit triggers one
bounded `GIT_TERMINAL_PROMPT=0` fetch for the detached analysis worktree.
Codex diagnosis, schema validation, cryptographic operations, and Git worktree
operations each run in bounded process groups, so a child process cannot
escape a timeout. Worktree
checkout additionally ignores system/global Git configuration, while
the bounded missing-source fetch retains the authenticated GitHub credential
helper and disables interactive prompts. Codex tool subprocesses inherit no caller environment and
receive only an isolated home,
the approved executable path, and fixed locale. A strict Codex permission
profile exposes only the detached source worktree plus minimal runtime files and
denies tool network access; the token and Codex auth home remain available only
to the Codex/MCP process and cannot be read by tool subprocesses. Project
execution rules are ignored, and `.codex/config.toml` or `.codex/hooks.json` in
the deployed source terminates analysis before Codex starts. Codex invokes the deployed revision's
`$wukongim-cloud-analysis` skill and only the allowlisted Analysis MCP tools.
Normal completion explicitly closes ingress;
failure and interruption issue at most one short, bounded close dispatch for
each request correlation without joining a long workflow from the EXIT trap.
An IPv4 rebind receives a new request correlation and therefore its own close.
Token expiry and
the scheduled sweeper remain independent backstops. Provider inventory reads
the rule deadline, so the sweeper preserves an unexpired local session and
revokes only expired or malformed windows. The compact Diagnosis
Result is printed locally and removed with the temporary session directory; an
operator-owned result path is used only by `finalize.sh` to consume the
validated diagnosis or provider-released state. No raw logs, metrics, profiles,
or Evidence Bundle are uploaded.

For a live run, `workload_inspect` reads only the bounded simulator-local
wkbench final summary. `healthy` requires that summary to be complete and
passed. The Diagnosis Result must preserve `workload_inspect` state and status
in its compact Observation reference; a missing, failed, or in-progress summary
cannot pass the healthy validator.

Use `--diagnostic-focus '<question>'` to narrow the investigation. The legacy
`--allow-fix-pr` option is accepted only for command-line compatibility and
prints a deferral notice. Neither `analyze.sh` nor `finalize.sh` changes code,
pushes a branch, waits for CI, or creates a PR while provider resources may
still be live. After exact Cleanup and independent `released`/empty-inventory
proof, use the normal repository review and publishing workflow as a separate
operation if the validated diagnosis requires a product fix.

## 4. Destroy or sweep

`Cloud Simulation - Cleanup` runs every 15 minutes. A manual dispatch with an
exact Run Identity performs protected early destruction. Success means the
adapter listed all supported tagged resource types after deletion and found
zero residual resources; a remaining billable resource fails the workflow.

The GitHub broker, provision, manual cleanup, and scheduled sweep share one
repository-wide concurrency group. After the broker finishes, the local
Analysis Session remains protected by its provider-observed rule deadline.
A sweep preserves unexpired `/32` rules, revokes expired or malformed windows,
and then evaluates immutable lease expiry.

Do not treat a deleted instance alone as cleanup success. The reconciliation
also covers independent disks, the simulator EIP, security rules, security
group, vSwitch, and VPC.

## 5. Required Alibaba canary and drills

Before enabling Tencent work, record green results for all of these manual,
bounded drills:

1. `small + 2h` provision, healthy analysis, and manual cleanup;
2. cancellation after quote, network, each host, EIP, transfer, and gate;
3. native instance auto-release followed by scheduled zero-residual sweep;
4. a deliberately stale analysis/SSH rule and a detached tagged disk;
5. one reclaimed cluster node and one lost simulator;
6. released and unknown Run Identity preflight;
7. one seeded product defect and one eligible Draft PR created only after
   ingress closure.

Use [cloud-simulation-drills.md](cloud-simulation-drills.md) as the signed
attestation template. Terraform, OSS/COS, or an Evidence Bundle are not part of
this process.
