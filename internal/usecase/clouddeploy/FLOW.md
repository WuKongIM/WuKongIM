# Cloud Deployment Use-Case Flow

`clouddeploy` owns the procurement-independent, content-addressed payload and
the provider-neutral activation contract used by the Cloud Lease Deployment
Action. It accepts only a validated non-secret Lease inventory projection; it
has no provider API, lifecycle permission, runtime credential, or workload
stage authority.

```text
trusted main control SHA + immutable source SHA
  -> runner builds Manager and Demo assets
  -> runner builds linux/amd64 product and control binaries
  -> runner adds checksum-pinned offline dependencies
  -> Seal writes the fixed Ubuntu 24.04 deployment intent and native templates
  -> static validation proves ELF architecture, required files, secret paths,
     fixed modes, no symlink or container dependency, and exact topology constants
  -> ordered file records produce one SHA-256 bundle digest
  -> Verify independently recomputes the same digest on every target host

active Lease Receipt + verified bundle manifest
  -> BuildPlan binds exact Lease, source, control, bundle, four roles, addresses,
     disks, expiry, and fixed topology into one digest
  -> RenderHostFiles produces Lease-specific native configuration without secrets
  -> install-offline verifies, mounts, renders, and prepares only the selected role
  -> activate-offline starts role infrastructure without starting the coordinator
  -> readiness reads effective topology config from all three nodes and proves
     host, cluster, load, proxy, and observer gates
  -> EvaluateReadiness emits one typed receipt or stable bounded failure
```

The bundle is deliberately free of secrets and Lease-specific configuration.
Its load-node payload includes 15-second Prometheus scraping with fixed
96-hour/150-GB retention, node metrics, and one root collector that exports
independent process metrics for every service, worker, coordinator, proxy, and
collector through node_exporter's textfile directory. Demo static, API, and
WebSocket paths share the same temporary Basic Authentication boundary while
Manager retains its own read-only application login.
The load host carries the non-restarting coordinator unit and its bounded
dependency gate, but Deployment deliberately leaves it dormant. Workload
orchestration consumes the successful Deployment Receipt and alone authorizes
the exact rehearsal, formal, or capacity-stage coordinator start.
The use case renders and validates Deployment Plans and readiness outcomes.
Disk discovery/mounting, systemd activation, SSH transfer, runtime credential
materialization, and live evidence collection remain host/Action adapters. The
Action cannot Quote, Acquire, Release, or otherwise mutate provider inventory.
The production Action mirrors the Fleet gates with a locally fakeable shell
adapter and authenticates its caller-supplied Artifact runs before executing
payload code.
The legacy
`internal/infra/cloudsim/deploy` bundle remains a separate compatibility path.
