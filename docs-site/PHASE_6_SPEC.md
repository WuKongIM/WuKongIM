# WuKongIM v3 Documentation — Phase 6 Specification

## Goal

Publish the bilingual server-operations path that follows deployment and
configuration. An operator must be able to use Manager safely, distinguish
process liveness from cluster readiness, expand and contract a cluster without
bypassing cluster semantics, create and verify recoverable backups, and choose
an upgrade procedure without assuming undocumented mixed-version or migration
compatibility.

## Published routes

- Operations overview
- Manager
- Health & Monitoring
- Scaling
- Backup & Restore
- Upgrade & Migration

Every route above has matching Chinese and English MDX and is included in
search, sitemap, LLM outputs, and per-page Markdown.

## Source-of-truth boundaries

- Manager is a privileged administrative listener. Its authentication,
  authorization, exposure, and audit boundaries are independent from product
  client traffic. Disabling Manager authentication does not grant anonymous
  mutation access.
- `/healthz` reports process liveness. `/readyz` reports whether the node may
  accept product traffic and returns HTTP 503 when it is not ready. Metrics,
  Top snapshots, debug routes, and Manager realtime views are distinct
  observability surfaces with separate enablement and exposure policies.
- Every deployment remains a cluster, including a single-node cluster. The
  physical hash-slot fence remains 256; scaling data nodes does not add or
  remove physical hash slots.
- A dynamically joined data node is not automatically assigned Slot replicas
  or leaders. Manager onboarding is an explicit operation, and Controller voter
  promotion is a separate explicit choice.
- Scale-in fails closed when health, runtime, Controller, Slot, Channel, or task
  evidence is missing or stale. Manager drains until authoritative status
  reports `safe_to_remove=true`; diagnostics then derives the
  `ready_to_remove` recommendation. Manager does not terminate infrastructure
  or call a Kubernetes scale-down operation.
- Backup planning and restore are managed in Manager, not through
  `wukongim.toml` or `WK_BACKUP_*` variables. Saving a plan is not a repository
  test. A backup is not published until all 256 hash slots are captured,
  verified, and marked complete.
- Restore is an online administrative workflow that enters cluster maintenance,
  stages and verifies all 256 slots, and either switches atomically or rolls
  back. It requires explicit restore permission and confirmation. The current
  format restores only the same cluster identity; it is not a portable
  new-cluster migration mechanism.
- A mixed-version rolling upgrade is allowed only when the exact release
  documentation declares that compatibility. Otherwise operators use a full
  maintenance window and avoid mixed versions. There is no general documented
  in-place v2-to-v3 storage migration contract or automatic migration tool.

## Validation

- Navigation tests freeze the newly published operations routes, require both
  locale variants, and keep Troubleshooting planned.
- Static-output validation confirms every published route appears in sitemap,
  search, LLM outputs, and per-page Markdown while the planned route remains
  excluded.
- Local validation runs the complete `bun run verify` workflow plus focused Go
  tests for API health, Manager, scaling orchestration, and backup/restore.
- Browser QA covers the operations entry and representative detail pages in
  both locales, including console output and horizontal overflow.

## Excluded

- Automated infrastructure provisioning, Kubernetes scale-down, DNS, firewall,
  TLS termination, or production cutover.
- Universal capacity numbers, workload-independent alert thresholds, or an
  automatic rebalancing promise.
- A portable cross-cluster restore, generic disaster-recovery service, or
  guaranteed v2-to-v3 storage conversion.
- Release-specific compatibility claims that are not present in that release's
  reviewed documentation.
- Symptom-led incident diagnosis and tool-specific troubleshooting procedures;
  Troubleshooting remains a planned route.

This is the Phase 6 boundary. Phase 7 later publishes the bilingual
Troubleshooting route; the historical exclusion above is not current status.
