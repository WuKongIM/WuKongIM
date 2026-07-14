# Cloud Simulation Control Flow

`internal/usecase/cloudsim` owns provider-neutral Simulation Run lifecycle
rules. It never imports a cloud SDK and never treats workflow-local state as
resource authority.

```text
trusted access/CLI
  -> ControlPlane
     -> validate exact Run Identity, preset, lease, cost ceiling and tags
     -> Provider.Inventory (concurrency and release truth)
     -> Provider.Quote (price, quota, spot and subnet capacity)
     -> Provider.Create/Transition/OpenAnalysis/CloseAnalysis/Destroy
```

`RunLocator` is a strict, bounded, non-diagnostic JSON record. Live status and
cleanup come from Provider inventory. A released-run decision additionally
requires a valid matching locator; provider inventory alone is not sufficient.
Before an empty inventory can become `released`, `Provider.Authority` must bind
the locator's provider, region, and account hash to the adapter's current
inventory authority; a wrong account or region therefore fails closed.

Lifecycle transitions are forward-only. The provisioning workflow persists
`provisioning -> ready -> running` after the bootstrap gate and workload start.
Entering `running` records the active workload deadline; provider reconciliation
projects an elapsed running deadline as `analysis_grace`. A failed bootstrap
enters `analysis_grace` only when the simulator MCP self-check is usable;
otherwise the workflow releases the run immediately.

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
