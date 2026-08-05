# Documentation Site Flow

## Responsibility

`docs-site` is the standalone Fumadocs application for the public WuKongIM v3
documentation. It owns the `/zh` and `/en` sites, their shared information
architecture, static search, SEO outputs, and machine-readable documentation
outputs. It does not own the legacy v2 site or product runtime documentation
under the repository-level `docs/` directory.

## Source and publishing flow

```text
lib/navigation.ts
  -> Fumadocs sidebars and top-level tabs
  -> static params for visible published/planned routes
  -> NAVIGATION.md planning reference

content/docs/**/*.mdx
  -> fumadocs-mdx source
  -> published domain and onboarding pages
  -> Orama search + sitemap + llms.txt + per-page Markdown
```

- Chinese and English MUST have the same menu structure.
- A page is `published` only when both locale variants exist and are ready.
- A `planned` route remains visible in navigation and renders a scope summary,
  but MUST be excluded from search, sitemap, and LLM outputs and MUST emit
  `noindex`.
- `lib/source.ts` filters generated MDX files through the navigation registry;
  unknown or still-planned content paths fail closed before any index consumer.
- Change `lib/navigation.ts`, then run `bun run navigation:write`; CI-style
  validation uses `bun run navigation:check`.
- `redirects.json` is only a phase-one seed. The complete legacy URL audit and
  host-specific redirect adapter belong to the migration/deployment phase.
- Phase 2 publishes the product overview, core-concepts overview, complete
  source-based quick start, and basic configuration overview. Their commands
  and defaults MUST stay aligned with the root README, `wukongim.toml.example`,
  and the embedded Chat Demo.
- Phase 3 publishes the integration overview, architecture, authentication,
  messaging, and webhook guidance. These pages MUST preserve the current
  product security and reliability boundaries: default app composition does
  not validate stored tokens, product HTTP routes require an external trust
  boundary, and webhook delivery is bounded and best-effort without a built-in
  signature header.
- Phase 4 publishes deployment selection, Docker, Linux, static multi-node,
  and production-checklist guidance. These pages MUST keep the repository
  Compose stack development-only, build artifacts from reviewed source without
  inventing an official image channel, use `/readyz` for traffic admission,
  preserve 256 hash slots and cluster-only semantics, and leave Kubernetes
  planned.
- Phase 5 publishes cluster, networking, storage, security, observability, and
  configuration-reference guidance. The bilingual reference MUST cover every
  public field returned by `internal/config.SchemaFields()` exactly once. The
  root `wukongim.toml.example` remains a development baseline rather than a
  runtime-default promise; configuration pages MUST distinguish listeners from
  advertised addresses, preserve cluster-only and 256-hash-slot semantics, and
  leave full operational procedures planned.
- Phase 6 publishes Manager, health and monitoring, scaling, backup and
  restore, and upgrade/migration guidance. Operations MUST use `/readyz` for
  traffic admission, keep physical hash slots fixed at 256, make dynamic-node
  onboarding explicit, and fail scale-in closed until authoritative status
  reports `safe_to_remove=true`; diagnostics then derives the
  `ready_to_remove` recommendation. Backup plans live only in Manager; saving is distinct from
  repository testing, restore is limited to the current cluster identity, and
  all 256 slots are verified before switch. Mixed-version rolling upgrades
  MUST require an exact release compatibility statement; no generic v2-to-v3
  in-place storage migration is promised.
- Phase 7 publishes symptom-led troubleshooting and the official-tool path.
  Investigation starts with the least expensive trustworthy evidence and keeps
  unknown or contradictory state fail-closed. `wkcli` operations retain Manager
  safety gates; `wkdb` remains node-local and offline, with `import` as its only
  storage-writing command; and `wkbench` is restricted to controlled benchmark
  clusters. Operations MCP remains a dedicated-credential, non-browser,
  closed-world observation surface with no write tool; its bounded
  `pprof_analyze` capture is the sole active observation.
- Phase 8 publishes the server-architecture path. It MUST distinguish 256
  stable physical hash-slot fences from logical Slot Raft Groups, Controller
  intent from observed Raft leadership, Slot metadata from Channel message
  logs, and durable Channel commit from post-commit effects. Client Gateway
  transport and node transport remain separate; UID presence is an in-memory,
  exact-target-fenced authority while concrete sessions remain owner-local.
  Older wiki architecture content is not a publication source unless it is
  recalibrated against the promoted packages.
- Phase 9 completes the guide foundation with product capabilities, use cases,
  clusters and nodes, messages, Channels, users and devices, conversations,
  and plugin extensions. Capability claims MUST remain workload-qualified;
  physical hash-slot fences remain distinct from logical Slot Raft Groups;
  durable commit remains distinct from delivery, acknowledgement, and user
  projections; and concrete Sessions remain owner-local. Plain non-command
  `NoPersist` is a compatibility terminal-success branch without realtime
  delivery, while only command-style `NoPersist` enters transient delivery.
  Plugins remain node-local: Send is synchronous and fail-closed by default,
  while Receive and PersistAfter are bounded post-commit effects.

## Static delivery

`next.config.mjs` uses Next.js static export. `bun run build` writes `out/`;
`bun run test:output` validates publishing boundaries against that artifact.
No deployment, DNS change, or production cutover is performed by these phases.
