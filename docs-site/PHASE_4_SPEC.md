# WuKongIM v3 Documentation — Phase 4 Specification

## Goal

Publish the bilingual server-deployment path that follows Quick Start and the
business-integration guide. An operator must be able to choose a supported
deployment shape, build and run the current server artifact, understand the
static multi-node cluster contract, verify traffic readiness, and identify the
work that remains before production.

## Published routes

- Deployment overview
- Choose a Deployment
- Docker
- Linux
- Multi-node Cluster
- Production Checklist

Every route above has matching Chinese and English MDX and is included in
search, sitemap, LLM outputs, and per-page Markdown. Kubernetes (Beta) remains
planned.

## Source-of-truth boundaries

- Build requirements and commands follow `go.mod`, the root READMEs, and the
  repository `Dockerfile`.
- The repository Dockerfile builds the current source tree into a local image.
  The documentation must not invent an official registry, image tag, release
  channel, or supply-chain guarantee.
- The root `docker-compose.yml` and `docker/conf/node*.toml` are a three-node
  development and validation environment. They expose fixed development
  credentials, benchmark/debug surfaces, host ports, and local bind mounts and
  must not be presented as a production manifest.
- Linux service examples are operator-owned reference units, not repository
  release artifacts. They use an unprivileged account, explicit configuration,
  persistent state, a runtime directory, graceful `SIGTERM`, and the
  application stop timeout.
- Every deployment is a cluster. A one-node topology is a single-node cluster
  and follows the same Controller, Slot, Channel, routing, and storage paths.
- Static multi-node nodes use unique node IDs and data directories while
  sharing the same ordered voter inventory. Peer addresses and advertised
  client addresses must be reachable from their consumers; wildcard listen
  addresses are not peer or client advertisements.
- The documented physical hash-slot fence remains 256. Replica counts must fit
  the available failure domains; copying the development value `3` is not a
  substitute for independent nodes and storage.
- `GET /healthz` reports process-level HTTP health. `GET /readyz` reports
  traffic readiness and returns `503` while cluster write routing or restore
  maintenance is not ready. Load balancers and rollout gates use `/readyz`.
- Configuration follows `wukongim.toml.example` and the loader contract:
  explicit `-config` is preferred, `WK_*` values override TOML, and list
  environment values replace the complete list as JSON.
- Product HTTP APIs, Manager, metrics, debug, benchmark, diagnostics, and node
  transport require separate exposure policies. The application does not
  provide production TLS termination or make the product HTTP API a trusted
  public boundary.
- Persistent state belongs on independent durable storage per node. Backup,
  restore, online scale-in, upgrades, and disaster recovery remain separate
  operations topics; the checklist must not imply they are complete merely
  because a process is ready.

## Validation

- The navigation test freezes the six newly published routes and requires
  matching Chinese and English MDX.
- Static-output validation confirms every published route appears in sitemap,
  search, LLM outputs, and per-page Markdown while Kubernetes stays excluded
  and noindex.
- Local validation runs the complete `bun run verify` workflow.
- Repository checks run the configuration, Docker Compose, and scripts test
  packages that protect the examples used by these pages.
- Browser QA covers both locales at the available desktop viewport, including
  published and planned routes, console output, and horizontal overflow. The
  shared Fumadocs responsive layout remains unchanged by this phase.

## Excluded

- Publishing or deploying an official container image.
- Shipping a production Compose, systemd, or Kubernetes manifest.
- DNS, certificates, load-balancer changes, secret distribution, firewall
  changes, or production cutover.
- Full monitoring, backup/restore, scaling, upgrade, migration, or
  troubleshooting procedures.
- Capacity numbers that are not backed by a workload-specific benchmark.
- Raster deployment diagrams; topology and lifecycle visuals remain
  maintainable text.
