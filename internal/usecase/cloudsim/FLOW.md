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
     -> Provider.Create/OpenAnalysis/CloseAnalysis/Destroy
```

`RunLocator` is a strict, bounded, non-diagnostic JSON record. Live status and
cleanup come from Provider inventory. A released-run decision additionally
requires a valid matching locator; provider inventory alone is not sufficient.

Analysis ingress accepts one IPv4 `/32`, expires within 50 minutes and the Run
Lease, and requires at least 30 minutes of lease remaining. `Sweep` reconciles
all expired unreleased runs and returns partial cleanup failures explicitly.
