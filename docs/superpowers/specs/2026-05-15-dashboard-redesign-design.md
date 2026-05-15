# Dashboard Redesign — Health-First Cockpit

- Status: Draft
- Owner: web (manager)
- Created: 2026-05-15
- Scope: `web/src/pages/dashboard/page.tsx` and supporting i18n / mock-data / shell components

## 1. Goal

Replace the current "five even metric cards + summary + alert rail + control queue table" layout with a **health-first cockpit** tuned for the on-call SRE. The redesigned dashboard answers — in this order — four questions a duty engineer asks the moment they open the page:

1. **Is anything wrong right now?** (one-glance verdict, < 0.5s)
2. **Where is it wrong?** (categorised incident list with deep links)
3. **Is the trend stable, recovering, or getting worse?** (sparkline pulse over the last 30 minutes)
4. **What does the cluster physically look like?** (per-node snapshot, no extra navigation needed)

This is a **visual / information-architecture redesign of one route** (`/dashboard`). It is not a backend redesign and does not propose new manager APIs. Sparkline trends use mocked time-series data; once a real metrics endpoint exists, the data source can be swapped without changing layout.

## 2. Non-Goals

- No new HTTP endpoints. We continue calling `getOverview / getTasks / getNodes / getChannelClusterSummary`. A new mock helper will produce sparkline data; the contract is structured so a real endpoint can replace it later.
- No changes to navigation, routing, auth, or any other page. `/dashboard` remains the route.
- No changes to the `shell` / `manager` reusable components beyond adding **net-new** primitives needed by this page (see §6). Existing components keep their API surface.
- No charts library swap. We continue using `recharts`, already a project dependency.
- Spec covers **light theme** behaviour. Dark theme already follows from existing CSS variables; redesign uses the same tokens, so no extra theming work is in scope.
- Real-time push, WebSocket streaming, and auto-refresh polling are out of scope. The page keeps the existing manual `Refresh` action.

## 3. User & Use Cases

Primary user: on-call WuKongIM operator opening `/dashboard` after a page or as a periodic check.

Use cases:

- **U1 — Verdict at a glance.** Operator opens the page; within one viewport on a 1440×900 laptop they see whether the cluster is healthy, degraded, or critical, plus the single most important counter (controller leader id and slot ready ratio).
- **U2 — Incident triage.** Operator sees the live incident list (slot quorum loss, retrying tasks, controller raft anomalies), each row carries a primary action that opens the relevant detail page.
- **U3 — Trend read.** Operator confirms whether the pulse is stable or trending bad over the last 30 min by looking at four sparklines (msg/s, connections, tx KB/s, RPC error rate).
- **U4 — Topology check.** Operator sees a one-line-per-node summary (status, role, slot count, raft watermark) without leaving the page.

## 4. Information Architecture

The page is composed top-to-bottom of **five regions**, each with a single responsibility:

```
┌──────────────────────────────────────────────────────────────────┐
│ R1  Health Hero                          (verdict + actions)     │
├──────────────────────────────────────────────────────────────────┤
│ R2  Realtime Pulse — 4 sparklines                                │
├──────────────────────────────────────────────────────────────────┤
│ R3  Slot/Channel Health Donuts │ R4  Active Incidents             │
│      (left, ~38%)              │       (right, ~62%)              │
├──────────────────────────────────────────────────────────────────┤
│ R5  Topology snapshot (one row per node)                         │
└──────────────────────────────────────────────────────────────────┘
```

On widths below `xl` (≤ 1279 px) regions stack: R1, R2, R3, R4, R5. On `xl` and up R3+R4 sit side-by-side as shown.

### R1 — Health Hero

A single dense bar replacing both the existing `PageHeader` chips block and the row of five metric cards.

- **Verdict pill** (left): color-coded chip with icon and label.
  - `healthy` (green) — no anomalies in `overview.anomalies`, no degraded controller raft, no channel cluster `no_leader > 0`.
  - `degraded` (warning) — any of: `slots.quorum_lost > 0`, `slots.leader_missing > 0`, `tasks.retrying > 0`, controller raft health is `snapshot_required | snapshot_transferring | append_catchup | compaction_degraded`, channel cluster `isr_insufficient > 0`.
  - `critical` (destructive) — any of: `tasks.failed > 0`, controller raft `restore_failed`, channel cluster `no_leader > 0`, all controller voters unreported.
  - The most severe condition wins (critical > degraded > healthy).
- **Verdict sentence** (one line): `"<N> incidents · controller leader <id> · <ready>/<total> slots ready · <alive>/<total> nodes alive"`. When the verdict is healthy: `"All systems nominal. Generated <time>."`
- **Right side**: `Refresh` button (existing behaviour) and a `Generated <time>` text. The "Inspect alerts" anchor button is removed — incidents are visible above the fold now.
- **Eyebrow row** (sub-line, mono caps): `Operations cockpit · Scope: <cluster scope text>`. Replaces the existing three eyebrow chips, condensed.

This region replaces today's `PageHeader` block entirely.

### R2 — Realtime Pulse

Four equal-width sparkline tiles in a single row at `xl`, two-by-two on `md`, one column on small screens.

| Tile | Headline | Sub | Source |
|---|---|---|---|
| Messages/s | latest value | `peak <p> · avg <a>` over window | mock (placeholder) |
| Connections | latest value | `+/- delta` vs window start | mock (placeholder) |
| TX KB/s | latest value | `peak <p> KB/s` | mock (placeholder) |
| RPC error rate | latest value `%` | `errors <n> / calls <n>` | mock (placeholder) |

Each tile uses a 60-point sparkline (one point per 30 s, 30-minute window). Stroke uses `--chart-1` for healthy series and `--chart-4` for the error-rate series. Tiles are flat: a small mono uppercase label, a large 28-px headline number, a sub-line in muted tone, and the sparkline filling the bottom.

Mock data is generated deterministically per render so tests are stable (see §7).

### R3 — Slot & Channel Health (left card)

A single `SectionCard` titled `"Slot & channel health"` containing:

- **Slot donut** — 64-slot donut showing `ready / quorum_lost / leader_missing / unreported` segments. Center label: `"<ready>/<total>"` and `"<percent>% ready"` underneath.
- **Counters strip** below the donut:
  - `Quorum lost: <n>` (warning tone if > 0)
  - `Leader missing: <n>` (warning tone if > 0)
  - `Unreported: <n>` (neutral)
- **Channel sub-block** — three labeled rows from `channelCluster`:
  - `Healthy: <n>` (green)
  - `ISR insufficient: <n>` (warning if > 0)
  - `No leader: <n>` (destructive if > 0)
- A single bottom-right link `"Open channel cluster health →"` → `/cluster/channels?tab=overview`.

This region replaces today's "Operations Summary" 4-tile grid plus the channel-health block.

### R4 — Active Incidents (right card)

A `SectionCard` titled `"Active incidents"` with a count badge next to the title (`(<n>)`), description `"Slot, task, and controller anomalies sampled from the manager overview endpoint."`.

Body is a vertical list of incident rows. The list is built in the following priority order, each row carrying:

- **Severity dot** (color matches the verdict scale)
- **Title** (e.g. `Slot 9 — quorum lost`)
- **Detail line** (mono): `desired [1,2,3] → current [1,2]` for slot anomalies, or `<kind> <step> · attempt <n>/<max> · last error: <msg>` for tasks
- **Right side**: a primary `Inspect` button that deep-links to the right page.

Priority order:

1. Slot quorum-lost anomalies (`overview.anomalies.slots.quorum_lost.items`) → button → `/cluster/slots?focus=<slot_id>`
2. Slot leader-missing anomalies (`overview.anomalies.slots.leader_missing.items`) → button → `/cluster/slots?focus=<slot_id>`
3. Slot sync mismatches (`overview.anomalies.slots.sync_mismatch.items`) → button → `/cluster/slots?focus=<slot_id>`
4. Failed tasks (`overview.anomalies.tasks.failed.items`) → button → `/cluster/tasks?focus=slot-<slot_id>`
5. Retrying tasks (`overview.anomalies.tasks.retrying.items`) → button → `/cluster/tasks?focus=slot-<slot_id>`
6. **Controller raft anomalies**, derived from `nodes.items`: any node with `raft_health` in `restore_failed | compaction_degraded | snapshot_required | snapshot_transferring | append_catchup` → button → `/controller?node_id=<n>`

A `View all` link at the bottom-right of the card body deep-links to `/cluster/diagnostics?tab=trace` when there are any items, hidden otherwise.

When the list is empty: render an empty-state strip (reusing the existing `noActiveAlertsTitle / Description` copy) — the same wording as today, but inside this new card. Icon: a small filled status dot in `--status-healthy`.

The deep-link query parameters listed above (`?focus=<slot_id>`, `?focus=slot-<slot_id>`) are URL contracts the dashboard sets; the receiving pages do **not** need to consume them in this redesign — they remain harmless if ignored. A follow-up task on the receiving pages can wire them up.

### R5 — Topology Snapshot

A `SectionCard` titled `"Topology snapshot"`. Body: one row per node from `nodes.items`, rendered as a CSS grid with these columns:

| dot | name (id) | status | role | slots | raft health | watermark | actions |
|---|---|---|---|---|---|---|---|

- **Dot** — colored by `node.status` (`alive` → healthy, `draining`/`suspect` → warning, `dead` → destructive).
- **Name** — `node-<n>` plus mono `(#<id>)` and the local marker if `is_local`.
- **Status** — reuses existing `StatusBadge`.
- **Role** — `leader` / `follower` derived from `controller.role`.
- **Slots** — `<count> slots · <leader_count> leader`.
- **Raft health** — `StatusBadge` of `controller.raft_health`. `unknown` shows muted `—`.
- **Watermark** — `first <f> / applied <a> / snapshot <s>`. Hidden when not reported.
- **Actions** — small `Open` icon-button → `/controller?node_id=<id>`. Always visible.

The row is a flex/grid line, not a separate card, so 3 nodes fit comfortably and 50 nodes still scroll cheaply.

## 5. Loading, Error and Edge States

| State | Behaviour |
|---|---|
| Initial load | Replace whole page body with the existing `ResourceState kind="loading"`. Page header remains. |
| Manager 403 | `ResourceState kind="forbidden"` with retry. Same as today. |
| Manager 503 | `ResourceState kind="unavailable"` with retry. Same as today. |
| Other errors | `ResourceState kind="error"` with retry. Same as today. |
| Refreshing | Existing behaviour — keep current data on screen, button shows `Refreshing…`. |
| Empty incidents | R4 shows the green "All systems nominal" empty strip; R1 verdict = healthy. |
| Zero nodes | R5 shows `ResourceState kind="empty"` inside the card. |
| Sparkline source missing | Mock data still generates so R2 always renders something. A muted `(mocked)` micro-tag shows in the corner of each R2 tile until real data is wired (see §6). |

## 6. Components

### Existing reused
- `PageContainer`
- `SectionCard` (used for R3, R4, R5)
- `StatusBadge`
- `ResourceState`
- `Button`, `Card*`
- `Link` (react-router)
- `recharts` (for sparkline + donut)

### Net-new (added under `web/src/pages/dashboard/components/`)

1. **`HealthHero`** — props `{ verdict, generatedAt, controllerLeaderId, slotsReady, slotsTotal, nodesAlive, nodesTotal, incidentCount, refreshing, onRefresh }`. Pure presentation.
2. **`PulseTile`** — props `{ label, value, valueSuffix?, sub, series: number[], tone: "default" | "danger", mocked?: boolean }`. Renders sparkline using `recharts <LineChart>` with no axes/grid for compactness.
3. **`SlotChannelHealth`** — props `{ slots, channelCluster }`. Renders donut + counters + channel block.
4. **`IncidentList`** — props `{ items: IncidentItem[] }`. Renders the prioritised list with the empty state.
5. **`TopologyRow`** — props `{ node }`. Renders a single grid row.
6. **`useDashboardPulse()`** hook (in `web/src/pages/dashboard/use-dashboard-pulse.ts`) — returns `{ messagesPerSec, connections, txBytesPerSec, rpcErrorRate }`, each `{ latest, peak, avg, series }`. Implementation is **deterministic mock** based on a seeded PRNG (`generated_at` timestamp as seed) so tests have stable values. Exposes a single integration point: when a real endpoint is added later, only this hook changes.

`IncidentItem` is the shared union shape:

```ts
type IncidentItem = {
  key: string                   // stable react key
  severity: "critical" | "warning" | "info"
  title: string                 // already i18n-formatted
  detail: string                // already i18n-formatted
  href: string                  // deep link
  ariaLabel: string             // for the inspect button
}
```

### Updated
- `web/src/pages/dashboard/page.tsx` becomes a thin orchestrator: fetches state, computes derived view-models, composes the five regions. No JSX longer than ~80 lines; layout details live in the components above.
- `web/src/pages/dashboard/page.test.tsx` is rewritten — see §7.

### File layout

```
web/src/pages/dashboard/
  page.tsx
  page.test.tsx
  use-dashboard-pulse.ts
  use-dashboard-pulse.test.ts
  view-model.ts                 // pure functions: verdict, incident list, topology row data
  view-model.test.ts
  components/
    health-hero.tsx
    pulse-tile.tsx
    slot-channel-health.tsx
    incident-list.tsx
    topology-row.tsx
```

## 7. Tests

The existing test file's behavioural intent must be preserved. The redesigned tests cover:

1. **Renders verdict, controller leader, slots ready, generated timestamp** — replaces the "renders overview metrics" test. Asserts `/cluster healthy|degraded|critical/i` appears.
2. **Renders the active incidents card with prioritised items** — given the existing `overviewFixture`, expects `Slot 9 — quorum lost`, `rebalance plan` retrying line, and a controller-raft incident for node 1 (`snapshot required`). Also asserts deep-link `href`s.
3. **Empty incidents → healthy empty strip** — same shape as today's "no anomalies" test; copy reused.
4. **Topology snapshot lists nodes** — for each fixture node: status badge, raft watermark for node 1 (`first 10 / applied 20 / snapshot 9`), and an `Open` action with `href="/controller?node_id=1"`.
5. **Refresh re-fetches all four endpoints** — preserved verbatim from today.
6. **Forbidden state** — preserved.
7. **Unavailable state** — preserved.
8. **Locale (zh-CN)** — preserved; updated to assert the redesigned section copy.
9. **`useDashboardPulse` returns deterministic series for the same `generated_at`** — new unit test in `use-dashboard-pulse.test.ts`.
10. **`view-model.buildIncidents` orders items by priority and produces correct hrefs** — new unit test in `view-model.test.ts`. Covers: quorum-lost > leader-missing > sync-mismatch > task-failed > task-retrying > controller-raft.
11. **`view-model.computeVerdict` returns expected severity for each combination** — new unit test.

All assertions reference i18n keys (already present or newly added), not hard-coded English strings, except where the test explicitly verifies localisation.

## 8. i18n

New keys (en + zh-CN):

```
dashboard.verdict.healthy
dashboard.verdict.degraded
dashboard.verdict.critical
dashboard.verdict.summary           // "{incidents} incidents · controller leader {id} · {ready}/{total} slots ready · {alive}/{total} nodes alive"
dashboard.verdict.summaryHealthy    // "All systems nominal."
dashboard.pulse.messagesPerSec
dashboard.pulse.connections
dashboard.pulse.txKbPerSec
dashboard.pulse.rpcErrorRate
dashboard.pulse.peakAvg             // "peak {peak} · avg {avg}"
dashboard.pulse.delta               // "{sign}{delta} vs 30m ago"
dashboard.pulse.errorsOverCalls     // "errors {errors} / calls {calls}"
dashboard.pulse.mockedTag           // "mocked"
dashboard.health.cardTitle          // "Slot & channel health"
dashboard.health.slotsCenter        // "{ready}/{total}"
dashboard.health.slotsCenterSub     // "{percent}% ready"
dashboard.health.quorumLost
dashboard.health.leaderMissing
dashboard.health.unreported
dashboard.incidents.cardTitle       // "Active incidents"
dashboard.incidents.cardCount       // "({count})"
dashboard.incidents.cardDescription
dashboard.incidents.viewAll
dashboard.incidents.inspect
dashboard.incidents.slotQuorumLostTitle      // "Slot {id} — quorum lost"
dashboard.incidents.slotLeaderMissingTitle
dashboard.incidents.slotSyncMismatchTitle
dashboard.incidents.taskFailedTitle           // "Task {kind} failed on slot {id}"
dashboard.incidents.taskRetryingTitle
dashboard.incidents.controllerRaftTitle       // "Controller raft {health} on node {id}"
dashboard.incidents.slotPeers                 // "desired [{desired}] → current [{current}]"
dashboard.incidents.taskDetail                // "{kind} {step} · attempt {attempt} · last error: {error}"
dashboard.incidents.controllerRaftDetail      // "first {first} / applied {applied} / snapshot {snapshot}"
dashboard.topology.cardTitle
dashboard.topology.local
dashboard.topology.slotsValue                 // "{count} slots · {leaders} leader"
dashboard.topology.watermarkUnreported
dashboard.topology.openNode                   // aria-label for the row Open button
```

The following existing keys are renamed (`dashboard.scopeSingleNodeCluster` → `dashboard.scope`) so the eyebrow row can carry an environment-agnostic label that does not assume a single-node cluster:

```
dashboard.scopeSingleNodeCluster   →   dashboard.scope
```

The following existing keys are removed because their owning components no longer exist:

```
dashboard.inspectAlerts                 // "Inspect alerts" anchor button gone
dashboard.controllerLeaderCardTitle / Description
dashboard.controllerRaftCardTitle / Description
dashboard.controllerRaftReported / Watermark / WatermarkUnavailable / Open / OpenForNode
dashboard.nodesCardTitle / Description / Summary
dashboard.readySlotsCardTitle / Description
dashboard.tasksCardTitle / Description / Summary
dashboard.metricControllerHint
dashboard.operationsSummaryTitle / Description
dashboard.nodesLabel / slotsLabel / tasksLabel / generatedLabel
dashboard.alertListTitle / Description
dashboard.controlQueueTitle / Description
dashboard.table.slot / kind / status / attempt / lastError
```

The following keys are kept and reused unchanged:

```
dashboard.title / description
dashboard.cockpitEyebrow
dashboard.generatedAtValue / Pending
dashboard.controllerLeaderValue / Pending
dashboard.channelHealthTitle / Total / Healthy / IsrInsufficient / NoLeader / Open
dashboard.noActiveAlertsTitle / Description
dashboard.slotValue
common.refresh / refreshing / inspect
```

## 9. Visual Tokens

The redesign uses the existing CSS variables only. Specifically:

- Verdict pill: `--success` (healthy), `--warning` (degraded), `--destructive` (critical) — at `bg-*/10`, `border-*/25`, `text-*` to match `StatusBadge` patterns.
- Sparkline default stroke: `--chart-1`. Error-rate stroke: `--chart-4`.
- Donut segments: ready `--chart-1`, quorum lost `--chart-4`, leader missing `--chart-3`, unreported `--chart-5`.
- Section headers continue using mono uppercase 12-px tracking-`[0.14em]` per existing `SectionCard`.

No new color tokens; no hard-coded hex.

## 10. Accessibility

- Health hero: verdict pill is `<span role="status" aria-live="polite">` so screen readers announce changes after refresh.
- Donut and sparklines: each chart has an `aria-label` summarising the same info as the text counters next to it. Charts are decorative; the source of truth for screen readers is text.
- Incident rows: each `Inspect` button has an `aria-label` formatted from the incident title.
- Topology rows: each `Open` action has an `aria-label` `dashboard.topology.openNode`.
- All color-coded status carries text — never color-only.

## 11. Migration & Rollout

This is a single-route redesign. There is no feature flag because:

- All anomaly logic and four endpoints are unchanged
- The page is operator-facing only (no end-user impact)
- Tests cover the new layout end-to-end before merge

Steps:

1. Add new components, hook, view-model, and i18n keys.
2. Rewrite `page.tsx` to use them. Remove obsolete i18n keys in the same change.
3. Rewrite `page.test.tsx` and add the new unit tests. All tests green.
4. Run `yarn build` and `yarn test`; sanity-check `/dashboard` in dev with the existing fixtures.
5. Commit on a feature branch and open a PR.

## 12. Open Questions

None — design choices intentionally constrained to existing API + design tokens. Sparkline mock will be replaced once a metrics endpoint exists; the hook is the single substitution point.
