# Cloud Deployment Use-Case Flow

`clouddeploy` owns the procurement-independent, content-addressed payload used
by the new Cloud Lease Deployment Action. It does not know provider inventory,
Lease identities, runtime IP addresses, credentials, or workload stage state.

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
```

The bundle is deliberately free of secrets and Lease-specific configuration.
Its load-node payload includes 15-second Prometheus scraping with fixed
96-hour/150-GB retention, node metrics, and one root collector that exports
independent process metrics for every service, worker, coordinator, proxy, and
collector through node_exporter's textfile directory. Demo static, API, and
WebSocket paths share the same temporary Basic Authentication boundary while
Manager retains its own read-only application login.
Deployment Plan rendering, disk mounting, service activation, and readiness
belong to the Deployment Action consumer. The legacy
`internal/infra/cloudsim/deploy` bundle remains a separate compatibility path.
