# Cloud Simulation Control Flow

`internal/usecase/cloudsim` owns provider-neutral Simulation Run lifecycle
rules. It never imports a cloud SDK and never treats workflow-local state as
resource authority.

```text
trusted access/CLI
  -> ControlPlane
     -> validate exact Run Identity, preset, lease, cost ceiling and tags
     -> Provider.Authority (credential-to-account binding before cleanup)
     -> Provider.Inventory (concurrency and release truth)
     -> Provider.Quote (price, quota, spot and subnet capacity)
     -> Provider.Create/Transition/OpenAnalysis/CloseAnalysis
     -> Provider.OpenPublicView/ClosePublicView/Destroy
```

`RunLocator` is a strict, bounded, non-diagnostic JSON record. Live status and
cleanup come from Provider inventory. A released-run decision additionally
requires a valid matching locator; provider inventory alone is not sufficient.
Before Destroy, Sweep, or an empty-inventory `released` decision,
`Provider.Authority` must bind the active credential to the adapter's provider,
region, and account hash. A wrong account or region therefore fails closed
before cleanup inventory is interpreted or resources are mutated.

Lifecycle transitions are forward-only. The provisioning workflow persists
`provisioning -> ready -> running` after the bootstrap gate and workload start.
Entering `running` records the active workload deadline; provider reconciliation
projects an elapsed running deadline as `analysis_grace`. A failed bootstrap
enters `analysis_grace` only when the simulator MCP self-check is usable;
otherwise the workflow releases the run immediately.

Cloud workload duration is allowlisted as `30m`, `2h`, `24h`, `48h`, or
`168h`. Only 48h/168h are standard stability durations. Standard runs fail
closed unless the dispatch supplies a completed 30-minute storage calibration
identity and measured worst-node retained bytes per message. The provision
workflow applies a 50% compaction margin, reserves 30% free space, and places
the resulting size into the provider config before quote and creation. Large
168h runs require a separate explicit cost confirmation.

The immutable node bundle configures 256 physical hash slots owned by 10
logical Slot Raft Groups. Bootstrap evidence records and gates both counts; the
healthy leader/replica totals remain aggregated across all 256 hash slots. A
healthy Slot leader is the non-zero actual Raft leader contained in the current
voter set while quorum is ready and runtime peers match the assignment.
`PreferredLeader` mismatch remains observable placement drift and is not a
Bootstrap Gate failure because Raft eligibility and the elected leader are
authoritative.
Prometheus uses a 15-second interval with 72h retention for runs through 48h
and 192h retention for 168h.

After `running` is persisted, provisioning uploads a bounded Finalization
Schedule containing only the exact Run Identity, workload deadline,
initial analysis time, and immutable lease expiry. The local `finalize.sh`
command waits for that schedule and delegates diagnosis to `analyze.sh`. A
validated `workload_inspect.state=in_progress` retains the run for another
bounded attempt while the lease can safely admit one. Once terminal evidence is
available, or diagnosis cannot complete, the command dispatches exact cleanup
and invokes the provider-backed released gate again to prove zero residual
inventory. A failed Schedule upload leaves an already-running workload in
`running`; deadline reconciliation, rather than workflow failure, projects it
to `analysis_grace`.

Analysis ingress accepts one IPv4 `/32`, expires within 50 minutes and the Run
Lease, requires at least 30 minutes of lease remaining, and rejects a second
unexpired window for the same run. Provider inventory reconstructs ingress
expiry from run-owned security rules. `Sweep` preserves future active windows,
closes expired or malformed deployment and analysis windows, and then
reconciles all expired unreleased runs; partial cleanup failures remain
explicit.

Public Cloud View ingress is a separate Run-Lease-bounded capability. It admits
exactly `0.0.0.0/0` on TCP/19443 only while the run is `ready`, `running`, or
`analysis_grace`, and never beyond the immutable Run Lease. `Sweep` closes an
expired or malformed public window, while `Destroy` closes it before deleting
run-owned resources. This capability does not change Analysis MCP semantics.

`cloud-sim-monitor.yml` is an observer-only workflow. It runs every 30 minutes
or for an explicitly dispatched Run Identity, discovers existing running runs,
and reads public Cloud View and Prometheus evidence. It never calls Create or
starts wkbench. Patrol evidence includes target liveness, 30-minute sustained
target loss, CPU, RSS, disk use, and bounded queue saturation; missing evidence
fails closed and remains distinct from the cleanup lease backstop.

`ValidateTencentAdmission` is a delivery-order gate, not a Tencent adapter. It
requires repository-owner-reviewed real Alibaba workflow references and
zero-residual proof for every accepted canary/drill before second-provider work
may begin. Fake-provider and unit-test results cannot create that attestation.
