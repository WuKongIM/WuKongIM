---
scope: package
summary: Seals provider-neutral offline deployment bundles, exact Lease plans, host files, and typed readiness receipts.
---

# Cloud Deployment Use Case Flow

## Responsibility

This package owns the procurement-independent content-addressed deployment
bundle and provider-neutral activation contract. It accepts only validated,
non-secret Lease inventory and has no provider lifecycle authority.

## Boundaries

- Bundles contain Ubuntu 24.04 native binaries, assets, pinned offline
  dependencies, and templates, but no secrets or Lease-specific configuration.
- Host transfer, disk mounting, systemd, runtime credentials, SSH, and live
  evidence are Action adapters; workload orchestration alone starts coordinators.
- Deployment cannot quote, acquire, release, sweep, or replace Lease provenance.

## Main Flows

1. Build and seal linux/amd64 product/control artifacts; validate ELF, required
   files, modes, topology, secret paths, symlinks, and container independence;
   hash ordered records into one bundle digest verified on each host.
2. `BuildPlan` binds Lease, source, control, bundle, four roles, addresses,
   disks, expiry, topology, quote line items, and budget into one immutable
   digest; render secret-free role files and perform idempotent offline install.
3. Activate infrastructure with coordinators dormant, read every host's
   effective topology and chrony evidence, apply host/cluster/proxy/observer
   gates, and emit a typed readiness receipt or stable bounded failure.

## Invariants and Failure Semantics

- Topology remains three service hosts plus one load host, 256 Hash Slots, and
  the reviewed rehearsal/formal workload after normalizing only run ID/stage.
- Service-node templates pin Slot Raft to a 50 ms tick, two-tick heartbeat, and
  40-tick election floor, matching the 100 ms heartbeat and two-second minimum
  election window used by the production default.
- Public Manager and Demo share temporary Basic Authentication; Manager keeps
  its own read-only login. Only safe GET/HEAD may retry upstreams; writes and
  WebSockets disable retries and connection reuse.
- Formal execution is one native process across soak, capacity, and recovery;
  it never restarts services/workers, clears data, or splices process lifetimes.
- Direct repair may pass one validated process-duration override only to the
  rehearsal unit; repository YAML and formal execution remain unchanged.
- Five-second cost and expiry guards enforce the admitted CNY 1,350 operational
  stop and CNY 1,500 hard limit.
- A control repair may redeploy only the same Lease, source, bundle, and sealed
  identity. Bootstrap-user, coordinator-state, and dependency-script repairs
  are allowed only for explicitly recognized frozen revision or file hashes;
  unknown compatibility content fails closed.
- The published analysis endpoint is non-secret and identity-bound; provider
  access grants remain owned by the separate analysis lifecycle.

## Read First

- [Bundle sealing](bundle.go)
- [Deployment planning](deployment.go)
- [Host runtime contract](runtime.go)
- [Native templates](templates.go)

## Update Triggers

Update this file when bundle contents, topology, plan identity, native services,
readiness, public routing, coordinator ownership, budgets, or compatibility changes.
