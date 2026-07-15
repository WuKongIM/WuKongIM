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
     -> Provider.Create/Transition/OpenAnalysis/CloseAnalysis/Destroy
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

`ValidateTencentAdmission` is a delivery-order gate, not a Tencent adapter. It
requires repository-owner-reviewed real Alibaba workflow references and
zero-residual proof for every accepted canary/drill before second-provider work
may begin. Fake-provider and unit-test results cannot create that attestation.
