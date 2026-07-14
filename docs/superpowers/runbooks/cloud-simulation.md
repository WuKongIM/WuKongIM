# Cloud Simulation Operations Runbook

This runbook operates the Alibaba-first cloud simulation system. It creates no
OSS bucket, image registry, long-lived AccessKey, or historical Evidence
Bundle. A released run is no longer analyzable.

## 1. One-time CloudShell bootstrap

CloudShell is used only to establish the first trust edge. The browser session
already has an Alibaba identity, so the repository CLI can use the default
credential chain without printing or exporting an AccessKey. Ordinary GitHub
workflows cannot create their own OIDC trust before this step exists.

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
`cloud-sim-analyze.yml` on `main` in `cloud-sim-analysis`. Configure required
reviewers on billable creation if desired, but never put a required reviewer on
`cloud-sim-cleanup`; its 15-minute lease reconciliation must remain unattended.

Alibaba RAM accepts only the OIDC `iss`, `aud`, and `sub` condition keys. After
the RAM apply succeeds, manually dispatch `Cloud Simulation - Configure OIDC
Subject` once from `main`. It idempotently configures the repository subject as
`repo + context + job_workflow_ref` and verifies the result, allowing each RAM
`oidc:sub` to bind the exact repository, Environment, workflow file, and main
branch. Do not run Provision, Analyze, or Cleanup until this workflow is green.

Set these non-secret repository or Environment Variables from the output:

- `ALIBABA_CLOUD_SIM_OIDC_PROVIDER_ARN`
- `ALIBABA_CLOUD_SIM_PROVISIONER_ROLE_ARN`
- `ALIBABA_CLOUD_SIM_ANALYZER_ROLE_ARN`
- `ALIBABA_CLOUD_SIM_OIDC_AUDIENCE`
- `ALIBABA_CLOUD_SIM_CONFIG_JSON`

Before storing `ALIBABA_CLOUD_SIM_CONFIG_JSON`, replace its
`account_id_hash` with `account_id_hash` from `bootstrap-result.json`. Keep the
cloud account number itself out of GitHub configuration. Store the dedicated,
budget-limited `OPENAI_API_KEY` secret in both `cloud-sim-analysis` and
`cloud-sim-remediation`; do not put cloud role variables in the remediation
Environment. Environment secrets are job-scoped, so the isolated remediation
job cannot reuse the analysis Environment's copy.

Bootstrap removal is protected:

```bash
GOWORK=off go run ./cmd/wkcloudbootstrap --config bootstrap.json remove
```

Removal refuses while any tagged Simulation Run is active. Run it only when
the Cleanup Workflow proves no remaining run inventory.

## 2. Provision a run

Dispatch `Cloud Simulation - Provision` from `main`. The source SHA must be a
40-character commit reachable from `origin/main`. Select a reviewed
`cloud-small`, `cloud-standard`, or `cloud-stress` scenario, a compatible
infrastructure preset, `2h`, `24h`, or `48h` active duration, bounded analysis
grace, and a hard CNY cost ceiling.

The build job has no cloud identity. The protected provision job obtains a
short-lived OIDC credential, quotes live price/capacity/quota, creates exactly
four spot hosts and their run network, transfers one sealed bundle through a
temporary simulator-only SSH `/32`, and starts `wkbench-run.service` only after
the complete three-node/256-slot gate passes. The run generates separate
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

## 3. Analyze an exact live run

Dispatch `Cloud Simulation - Analyze` with the exact Run Identity. The workflow
first resolves the unique 90-day Run Locator and compares it to current
Alibaba inventory. Before empty inventory can mean `released`, STS verifies the
current OIDC caller's Alibaba account hash and the adapter verifies the exact
region against the locator; stale or cross-account configuration fails closed.

- A missing or ambiguous locator reports `unknown_run`.
- A valid locator plus empty provider inventory reports `released`, prints
  `Simulation Run <run_id> 已由云厂商确认自动销毁，当前没有可分析的实时数据；分析已终止。`, and stops before Codex.
- Existing resources with an unreachable MCP report `insufficient_evidence`,
  never `released`.

For a live run, the workflow opens one temporary MCP `/32`, pins the
run-specific CA fingerprint from protected resource tags, verifies the public
IP SAN, and exchanges its exact GitHub OIDC identity for a non-renewable
Analysis Token. Codex invokes the repository
`$wukongim-cloud-analysis` skill and only the allowlisted Analysis MCP tools.
Ingress closes unconditionally.

For a live run, `workload_inspect` reads only the bounded simulator-local
wkbench final summary. `healthy` requires that summary to be complete and
passed. The Diagnosis Result must preserve `workload_inspect` state and status
in its compact Observation reference; a missing, failed, or in-progress summary
cannot pass the healthy validator.

`allow_fix_pr=true` permits a second, fresh job only for a high-confidence,
testable `product_defect`. That job has repository write permission but no
cloud identity, MCP token, MCP configuration, SSH, or live access. It may open
a tested Draft PR and never merges it.

## 4. Destroy or sweep

`Cloud Simulation - Cleanup` runs every 15 minutes. A manual dispatch with an
exact Run Identity performs protected early destruction. Success means the
adapter listed all supported tagged resource types after deletion and found
zero residual resources; a remaining billable resource fails the workflow.

Provision, live analysis, manual cleanup, and scheduled sweep share one
repository-wide concurrency group. A sweep can therefore revoke stale
deployment and Analysis `/32` rules on active runs without interrupting a
legitimate job, before it evaluates immutable lease expiry.

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
