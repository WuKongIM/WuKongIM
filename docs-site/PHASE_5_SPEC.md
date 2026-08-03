# WuKongIM v3 Documentation — Phase 5 Specification

## Goal

Publish the bilingual server-configuration path that follows deployment. An
operator must be able to select a configuration source, distinguish required,
example, default, and runtime-derived values, configure cluster and listener
identity safely, place state on durable storage, protect privileged surfaces,
and enable bounded observability without treating the development example as
a production profile.

## Published routes

- Nodes & Cluster
- Networking & Client Access
- Messages & Storage
- Security & Access
- Logs & Observability
- Configuration Reference

Every route above has matching Chinese and English MDX and is included in
search, sitemap, LLM outputs, and per-page Markdown.

## Source-of-truth boundaries

- `internal/config.SchemaFields()` is the exhaustive public mapping among TOML
  paths, canonical `WK_*` environment keys, value kinds, and redaction flags.
  The bilingual reference pages must contain every returned field exactly once.
- `wukongim.toml.example` is a loadable development baseline. Its explicit
  values are examples, not a promise that omitted fields have the same runtime
  defaults. Some zero or omitted values are resolved from topology or hardware.
- The only always-required startup fields are `node.id`, `node.data_dir`, and
  `cluster.listen_addr`. Seed joining adds its own conditional requirements.
- Explicit `-config` is preferred. Without it, lookup remains
  `./wukongim.toml`, `./conf/wukongim.toml`, then
  `/etc/wukongim/wukongim.toml`. Environment values override TOML.
- Unknown TOML paths and unknown `WK_*` variables fail startup. List values in
  environment variables are JSON and replace the complete list.
- Every deployment remains a cluster. A one-node topology is a single-node
  cluster. The physical hash-slot fence remains 256; logical Slot Raft Group
  count is a separate initialization choice.
- Listen addresses, peer-advertised addresses, and client-advertised addresses
  are different contracts. Wildcard listen addresses are never advertised.
- Each node owns an independent data directory. Retention, concurrency, queue,
  and batch fields are workload-sensitive controls, not universal tuning
  recommendations or capacity promises.
- Manager credentials, cluster join tokens, benchmark tokens, and configured
  users are secrets. Product HTTP APIs still require an external trust boundary,
  and production TLS termination remains outside the application.
- App-managed Prometheus is optional. Manager, metrics, top, debug, benchmark,
  and diagnostics surfaces require separate exposure policies.
- Configuration is loaded during startup. The documentation must not imply
  general hot reload; operators restart one node at a time and wait for
  `/readyz` before restoring traffic.

## Validation

- Navigation tests freeze the six newly published routes and require matching
  Chinese and English MDX.
- A Go contract test checks both reference pages against every field returned
  by `config.SchemaFields()`.
- Static-output validation confirms every published route appears in sitemap,
  search, LLM outputs, and per-page Markdown.
- Local validation runs the complete `bun run verify` workflow plus focused
  `internal/config` tests.
- Browser QA covers both locales at a desktop viewport, console output, and
  horizontal overflow on the long reference tables.

## Excluded

- Publishing production-ready configuration values or workload-independent
  capacity numbers.
- Kubernetes manifests, secret distribution, TLS termination, firewall rules,
  DNS, or production cutover.
- Full monitoring, scaling, backup/restore, upgrade, migration, and incident
  procedures; those remain operations phases.
- Documenting internal-only values that are absent from the public schema.
- Adding runtime configuration fields or changing loader behavior.
