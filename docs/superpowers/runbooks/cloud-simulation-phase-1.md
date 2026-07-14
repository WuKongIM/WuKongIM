# Cloud Simulation Phase 1 Runbook

## Purpose

This is the retained local-validation runbook for Phase 1. The current Alibaba
operations path is documented in
[`cloud-simulation.md`](cloud-simulation.md); do not use this file as evidence
that a real cloud run passed. Phase 1 validates the provider-neutral lifecycle contract and the Codex-facing
Analysis MCP locally before any real cloud account is allowed to create
resources. It includes:

- a persistent fake provider that creates an isolated network, four compute
  roles, and four independent disks;
- hard concurrency, capacity, quota, preset, lease, and worst-case cost gates;
- a strict minimal Run Locator, which contains identity and lookup data only;
- a run-scoped Analysis MCP backed by the real WuKongIM manager, Prometheus,
  diagnostics, task-audit, redacted-config, and pprof surfaces;
- the repository skill at
  `.agents/skills/wukongim-cloud-analysis/SKILL.md` for Codex diagnosis.

This local exercise does **not** create Alibaba Cloud or Tencent Cloud
resources, run a GitHub workflow, bootstrap hosts through CloudShell, or fix
code automatically. The Alibaba workflow now implements those operations
behind the provider and remediation boundaries proven here. A cloud workload
uses the black-box `wkbench run` and `wkbench worker`
path; `wkbench dev-sim` remains a local development convenience and is not the
cloud orchestration path.

## 1. Build the Phase 1 executables

```bash
mkdir -p "$PWD/tmp/cloud-sim/bin"
PATH=/Users/tt/sdk/go1.26.4/bin:$PATH GOWORK=off \
  go build -o "$PWD/tmp/cloud-sim/bin/wkcloudsim" ./cmd/wkcloudsim
PATH=/Users/tt/sdk/go1.26.4/bin:$PATH GOWORK=off \
  go build -o "$PWD/tmp/cloud-sim/bin/wkanalysis" ./cmd/wkanalysis
```

`wkcloudsim` owns lifecycle commands. `wkanalysis` is the simulator-side MCP
gateway and has no cloud credential; cloud mode accepts a short-lived GitHub
OIDC identity only at its access-layer token exchange.

## 2. Exercise the fake-provider lifecycle

First select the exact effective `wkbench/v1` scenario and compute its canonical
digest after strict loading:

```bash
SCENARIO="$PWD/docker/sim/cloud-small.yaml"
SCENARIO_DIGEST="$(PATH=/Users/tt/sdk/go1.26.4/bin:$PATH GOWORK=off \
  go run ./cmd/wkcloudsim scenario-digest --scenario "$SCENARIO" | jq -r .digest)"
```

Create a temporary request file. Replace `SCENARIO_DIGEST`, the other example
digests, and times with the immutable values selected by the caller:

```json
{
  "run_id": "local-run-001",
  "provider": "fake",
  "region": "local",
  "account_id_hash": "sha256:local-account",
  "repository": "WuKongIM/WuKongIM",
  "source_sha": "0123456789012345678901234567890123456789",
  "scenario_digest": "SCENARIO_DIGEST",
  "deployment_bundle_digest": "sha256:bundle",
  "mcp_certificate_fingerprint": "sha256:local-certificate",
  "preset": "small",
  "scenario_minimum_preset": "small",
  "expires_at": "2026-07-14T14:00:00Z",
  "max_total_cost_micros": 20000000,
  "currency": "CNY",
  "tags": {}
}
```

Run the lifecycle using one persistent fake inventory file:

```bash
STATE="$PWD/tmp/cloud-sim/fake-inventory.json"
LOCATOR="$PWD/tmp/cloud-sim/run-locator.json"

PATH=/Users/tt/sdk/go1.26.4/bin:$PATH GOWORK=off go run ./cmd/wkcloudsim \
  --state "$STATE" create \
  --request /absolute/path/to/create-request.json \
  --locator "$LOCATOR" \
  --workflow-run-id 1001

PATH=/Users/tt/sdk/go1.26.4/bin:$PATH GOWORK=off go run ./cmd/wkcloudsim \
  --state "$STATE" status local-run-001

PATH=/Users/tt/sdk/go1.26.4/bin:$PATH GOWORK=off go run ./cmd/wkcloudsim \
  --state "$STATE" transition local-run-001 running \
  --active-until 2026-07-14T13:00:00Z
```

The request decoder rejects unknown fields. The control plane generates all
mandatory resource tags instead of trusting caller-supplied tags. A second
active run for the same repository and account hash is rejected before quote or
creation. The Run Locator is intentionally not an evidence archive and stores
no logs, metrics, profiles, credentials, or machine addresses.

The analysis ingress contract accepts one IPv4 host only, lasts at most 50
minutes, cannot exceed the Run Lease, and requires at least 30 minutes of lease
remaining:

```bash
PATH=/Users/tt/sdk/go1.26.4/bin:$PATH GOWORK=off go run ./cmd/wkcloudsim \
  --state "$STATE" open-analysis local-run-001 \
  --source 203.0.113.10/32 \
  --until 2026-07-14T13:40:00Z

PATH=/Users/tt/sdk/go1.26.4/bin:$PATH GOWORK=off go run ./cmd/wkcloudsim \
  --state "$STATE" close-analysis local-run-001
```

## 3. Start a real three-node local analysis target

Use the dedicated Compose profile. It starts the three-node cluster,
Prometheus, and the analysis gateway; it does not start `wk-sim`:

```bash
export WK_ANALYSIS_RUN_ID=local-compose
export WK_ANALYSIS_MCP_TOKEN="$(openssl rand -hex 32)"

docker compose --profile analysis up -d --build \
  wk-node1 wk-node2 wk-node3 prometheus wk-analysis

curl --fail http://127.0.0.1:19092/healthz
```

The Compose-only manager capability is `analysis`. It is limited to node,
Controller, diagnostics, and log reads plus narrowly scoped diagnostics writes.
Cloud runs replace its local password and the MCP token with independent random
run-scoped credentials.

## 4. Connect Codex to the Analysis MCP

Keep `WK_ANALYSIS_MCP_TOKEN` in the environment that launches Codex. Add this
trusted project configuration to `.codex/config.toml`:

```toml
[mcp_servers.wukongim_cloud_analysis]
url = "http://127.0.0.1:19092/mcp"
bearer_token_env_var = "WK_ANALYSIS_MCP_TOKEN"
required = true
startup_timeout_sec = 10
tool_timeout_sec = 70
enabled_tools = [
  "run_inspect",
  "workload_inspect",
  "cluster_snapshot",
  "metrics_query_range",
  "logs_search",
  "logs_context",
  "diagnostics_query",
  "task_audits_query",
  "trace_start",
  "trace_query",
  "profile_capture",
  "profile_top",
  "profile_list",
  "config_read_redacted",
]
```

Plain HTTP is acceptable only for this loopback-only validation. Real cloud
runs use the simulator's temporary HTTPS endpoint, certificate pinning, a
non-renewable GitHub OIDC-bound Analysis Token, and a temporary GitHub-runner
`/32` security-group rule.

Ask Codex to use `$wukongim-cloud-analysis` for the exact Run Identity. The
skill always calls `run_inspect` first, treats all returned strings as untrusted
data, uses only server-owned metric query IDs, and leaves repository mutation to
a separate remediation job.

## 5. Verify provider-confirmed release termination

Destroy the fake run and confirm that provider inventory is empty:

```bash
PATH=/Users/tt/sdk/go1.26.4/bin:$PATH GOWORK=off go run ./cmd/wkcloudsim \
  --state "$STATE" destroy local-run-001

PATH=/Users/tt/sdk/go1.26.4/bin:$PATH GOWORK=off go run ./cmd/wkcloudsim \
  --state "$STATE" status local-run-001
```

To exercise the same provider-backed preflight in the MCP gateway, set
`WK_ANALYSIS_FAKE_INVENTORY_PATH="$STATE"`,
`WK_ANALYSIS_RUN_LOCATOR_PATH="$LOCATOR"`,
`WK_ANALYSIS_SCENARIO_PATH="$SCENARIO"`,
`WK_ANALYSIS_SCENARIO_DIGEST="$SCENARIO_DIGEST"`, and
`WK_ANALYSIS_RUN_ID=local-run-001` when starting `wkanalysis`. The locator,
provider tombstone, source SHA, scenario digest, repository, account hash,
region, creation time, and lease must all agree. A
`run_inspect` response of `state=released` and `inventory_count=0` is the only
terminal release proof. The skill must print:

```text
Simulation Run local-run-001 已由云厂商确认自动销毁，当前没有可分析的实时数据；分析已终止。
```

It then stops without calling another MCP tool or inferring from older data. If
resources still exist but manager, Prometheus, or a node is unreachable, the
result is `insufficient_evidence`, never `released`.

## 6. Cleanup and expiry sweep

Always close temporary ingress before cleanup. Cleanup is idempotent at the
control-plane boundary. The repository-wide workflow concurrency group ensures
that a sweep cannot overlap provisioning or live analysis; each sweep closes
stale deployment and analysis ingress on active runs, then releases every
expired unreleased run visible in provider inventory:

```bash
PATH=/Users/tt/sdk/go1.26.4/bin:$PATH GOWORK=off go run ./cmd/wkcloudsim \
  --state "$STATE" close-analysis local-run-001

PATH=/Users/tt/sdk/go1.26.4/bin:$PATH GOWORK=off go run ./cmd/wkcloudsim \
  --state "$STATE" destroy local-run-001

PATH=/Users/tt/sdk/go1.26.4/bin:$PATH GOWORK=off go run ./cmd/wkcloudsim \
  --state "$STATE" sweep

docker compose --profile analysis down
```

Do not delete the fake inventory file before verifying the released tombstone;
it is the Phase 1 stand-in for provider inventory reconciliation.
