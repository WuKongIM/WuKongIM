# WuKongIM v3 Documentation — Phase 7 Specification

## Goal

Publish the bilingual troubleshooting and official-tools path. An operator
must be able to start from a symptom, collect the least expensive trustworthy
evidence, choose the correct repository tool, and stop when evidence is stale,
missing, or contradictory. Diagnostic guidance must not turn observation into
automatic remediation or broaden a local tool into a cluster-wide authority.

## Published routes

- Troubleshooting
- Tools overview
- wkcli
- wkdb
- wkbench
- Diagnostics

Every route above has matching Chinese and English MDX and is included in
search, sitemap, LLM outputs, and per-page Markdown.

## Source-of-truth boundaries

- Troubleshooting starts with `/readyz` and its reason, then correlates Manager,
  metrics, logs, Top, and bounded diagnostics. `/healthz` proves only process
  liveness. Unknown, stale, or contradictory evidence remains a stop condition.
- `wkcli` stores API contexts, reads node-local Top snapshots, runs lightweight
  send checks and controlled simulations, and operates dynamic-node lifecycle
  through public Manager HTTP. It does not start or stop server processes and
  does not write Controller or Slot state directly.
- `wkdb` is local and offline. `query`, `repl`, and `diff` are read-only;
  `export` reads source stores and writes only its bundle output; `import` is the
  sole storage-writing command and is restricted to an offline target. No
  command returns a global cluster view or an online-consistent cluster backup.
- `wkbench` is a black-box workload driver for controlled benchmark clusters.
  It uses public, benchmark-only, and WKProto endpoints without importing server
  internals. Most workflows require the benchmark API; exposing it publicly or
  using production as an unbounded load target is outside the supported path.
- Logs, Prometheus metrics, Top, Manager views, retained diagnostics, pprof, and
  the Operations MCP answer different questions and retain separate access and
  cost boundaries. Debug, profiling, and load generation are authorized,
  isolated, time-bounded activities.
- The embedded Operations MCP is stateless and read-only except that
  `pprof_analyze` performs a bounded active observation. It accepts dedicated
  `wko_*` credentials rather than Manager JWTs, rejects non-empty browser
  Origins, exposes no general command, URL, path, PromQL, SQL, or write tool,
  and fails unavailable observations to an `unknown` verdict.
- Every deployment remains a cluster, including a single-node cluster, and the
  physical hash-slot fence remains 256.

## Validation

- Navigation tests freeze all newly published routes, require both locale
  variants, and keep Architecture planned.
- Static-output validation confirms every published route appears in sitemap,
  search, LLM outputs, and per-page Markdown while planned routes remain
  excluded.
- Local validation runs the complete `bun run verify` workflow plus focused Go
  tests for wkcli, wkdb, wkbench, and Operations MCP observation contracts.
- Browser QA covers the troubleshooting and tools paths in both locales,
  including console output and horizontal overflow.

## Excluded

- Automatic remediation, topology mutation from diagnostic output, or a
  universal incident decision engine.
- Online mutation through wkdb, a cluster-wide wkdb snapshot, or treating an
  import bundle as a Manager backup archive.
- Production load generation, universal capacity numbers, or treating peak QPS
  from one environment as sustainable capacity elsewhere.
- Operations MCP write tools, browser access, arbitrary queries or commands,
  raw profile download, and automatic credential or network provisioning.
- Cloud Simulation and architecture internals; Architecture remains planned for
  a later phase.
