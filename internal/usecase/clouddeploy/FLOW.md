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
     host, cluster, load, proxy, and observer gates; host clock evidence comes
     directly from each Ubuntu chrony daemon instead of the untrusted runner clock
  -> EvaluateReadiness emits one typed receipt or stable bounded failure
```

The bundle is deliberately free of secrets and Lease-specific configuration.
Its load-node payload includes 15-second Prometheus scraping with fixed
96-hour/150-GB retention, node metrics, and one root collector that exports
independent process metrics for every service, worker, coordinator, proxy, and
collector through node_exporter's textfile directory. The bounded host-metrics
endpoint also forwards only those closed process families from the root-owned
textfile, allowing the lifecycle observer to persist per-process uptime,
cumulative CPU jiffies, and RSS without granting shell access. The endpoint
rejects the textfile when either its mtime or embedded collector-success time
is more than 45 seconds old. Demo static, API, and
WebSocket paths share the same temporary Basic Authentication boundary while
Manager retains its own read-only application login.
The successful receipt returns exact `http://<load-eip>/` Manager and
`http://<load-eip>/demo/` Demo URLs. Safe GET/HEAD proxy routes may retry a
different healthy upstream; write routes and WebSocket upgrades use separate
reverse-proxy handlers with load-balancer retries and upstream connection reuse
disabled. This also prevents the underlying HTTP transport from replaying an
otherwise idempotent-looking write after a stale reused connection.
The native Caddy unit validates the fully rendered configuration before every
start, so malformed route or matcher syntax fails activation instead of serving
a partial public surface.
The load host carries separate non-restarting formal and rehearsal coordinator
units plus their bounded dependency gate. The sealed formal and rehearsal YAML
must be byte-equivalent after normalizing only `run_id` and `stage`; both retain
the exact 10,000-online/250,000-new-user/2,000-SEND/s workload and thresholds.
The formal unit directly owns one native `wkbench formal-chain` process that
runs the 72-hour Soak, derives the capacity configuration from that exact
passing report, and continues the at-most-eight-hour staircase plus 30-minute
recovery with the same worker fence, generation, lifecycle loop, observation
source, and dataset. It never restarts service or worker processes, clears
data, or splices a second process lifetime. The immutable Deployment Plan
carries the admitted ¥1,500/¥1,350 budget envelope, Lease creation/expiry
instants, and the exact provider-neutral quote line items. The Action seals
those values plus a base64 JSON line-item projection into the root-only load
environment. The rehearsal runner and formal-chain both verify the line-item
sum and closed charge-kind vocabulary. Rehearsal requires its two-hour run plus
a one-hour cleanup reserve; formal-chain requires at least 81.5 hours
remaining. Both use the same envelope for five-second accrued-cost and expiry-
reserve guards through rehearsal, Soak, and capacity.
After exact zero-inventory cleanup, stage orchestration carries only
conservative accrued cost into the next Quote: held host hours are rounded up,
observed public traffic is rounded to GiB when available (otherwise the full
quoted allowance is reserved), and retention risk is charged in full. It does
not debit the complete multi-hour Quote for a short failed deployment.
Deployment deliberately leaves both coordinator units dormant. Workload
orchestration consumes the successful Deployment Receipt and alone authorizes
the exact stage-specific coordinator start. Remote systemd, rather than a
GitHub runner, owns the measured execution and its report files.
Before that ownership transfer, the Deployment Action may be re-invoked on the
same Lease with the same source, content-addressed bundle, and sealed SSH
identity after a request-bound protected-main control fix. Host installation is
idempotent for an already mounted expected ext4 data disk and overwrites only
the reviewed role payload and per-deployment runtime credentials. The Action
still cannot replace bundle provenance or mutate the Lease lifecycle.
The one exact `4daf86e4a88478ccdecd9675acee8414810413be` orchestrator revision
predates the `wkdeploy` bootstrap-user correction. Its repair deployment adds
an idempotent `wukong` compatibility account carrying only the already admitted
public keys and equivalent sudo policy so that the already-running orchestrator
and its finalizer can finish without replacing the Lease. That same frozen
revision also treats `systemctl reset-failed` as an always-successful operation;
on systemd versions that reject a never-failed dormant unit, the repair Action
briefly overlays the selected coordinator with `/bin/false`, proves the failed
state, removes the overlay, and leaves the real unit failed for the frozen
orchestrator to reset immediately before its authorized start. The current
orchestrator treats reset as idempotent. Other control revisions do not create
the account or prime coordinator state. The frozen dependency script also
predates authenticated worker health endpoints. Its exact legacy file hash or
the exact first authenticated compatibility hash may be replaced atomically
with the current token-bearing equivalent after load host preparation; unknown
content fails closed. The immutable template remains
unchanged while this active source/bundle identity is in use; a later bundle
revision must carry the same authenticated probe directly. The compatibility
gate also waits until the 15-second process collector exposes exactly one
up/CPU/RSS row for the selected stage unit; formal preflight therefore cannot
race a stale pre-start process snapshot and misclassify it as disk ambiguity.
The use case renders and validates Deployment Plans and readiness outcomes.
Disk discovery/mounting, systemd activation, SSH transfer, runtime credential
materialization, and live evidence collection remain host/Action adapters. The
Action opens only the exact unexpired per-Lease deployment Ed25519 identity
sealed to the `cloud-deployment` Environment wrapping key; the standing
wrapping private key is never authorized on a host, and the Lease private key
is removed from every runner after use. The independently generated Codex
diagnostic private key never enters GitHub. The
Action cannot Quote, Acquire, Release, or otherwise mutate provider inventory.
The production Action mirrors the Fleet gates with a locally fakeable shell
adapter and authenticates its caller-supplied Artifact runs before executing
payload code.
The legacy
`internal/infra/cloudsim/deploy` bundle remains a separate compatibility path.
