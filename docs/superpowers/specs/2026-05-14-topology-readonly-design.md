# Topology Readonly Page Design

## Goal

Replace the `/topology` placeholder with a read-only topology MVP that helps operators understand the global cluster and Slot placement using existing manager APIs.

This MVP intentionally avoids new backend endpoints and write operations. It is a visibility feature, not a topology control surface.

## Scope

In scope:

- Render `/topology` from existing manager read APIs:
  - `GET /manager/overview`
  - `GET /manager/nodes`
  - `GET /manager/slots`
- Show Controller leader, node health, Slot counts, and Slot anomaly summary.
- Show node cards with controller voter/local/status and Slot replica/leader counters.
- Show a Slot placement matrix with leader, desired peers, current peers, quorum, and sync state.
- Support a node filter that focuses Slots where the node is leader, desired peer, or current peer.
- Link users to existing detail pages (`/nodes`, `/slots`) instead of duplicating write controls.
- Map 403/503 errors to existing `ResourceState` states.
- Update page-shell tests, i18n strings, and status docs.

Out of scope:

- New `GET /manager/topology` endpoint.
- Channel-level replica topology.
- Graph layout libraries or draggable canvas interactions.
- Write operations such as rebalance, leader transfer, drain, repair, or onboarding.
- Persisted topology snapshots or history.

## User Experience

The page keeps the existing manager shell style and uses simple, dense operations views:

1. Header badges show the page scope and that the view is read-only.
2. Summary cards show:
   - Controller leader
   - Total nodes
   - Total Slots
   - Slot anomalies
3. Node topology cards show:
   - Node ID/name/address
   - Health/status
   - Controller voter or non-voter
   - Local marker
   - Slot replicas/leaders/followers
   - Runtime summary when available
4. Slot placement table shows:
   - Slot ID
   - Leader
   - Desired peers
   - Current peers/current voters when available
   - Quorum and sync status
   - Hash Slot ownership summary when available
5. Node filter:
   - Defaults to all nodes.
   - Selecting a node keeps only Slots that mention that node in leader, desired peers, current peers, or current voters.
   - Summary text reports the filtered Slot count.

The page should not imply that it has complete channel-level topology. Copy should say this is the global cluster and Slot topology MVP.

## Data Flow

`TopologyPage` loads three resources in parallel:

```text
/topology
  -> getOverview()
  -> getNodes()
  -> getSlots()
  -> derive view model in page/component helpers
  -> render summary, node cards, and Slot matrix
```

Derived values:

- `controllerLeaderId` from `overview.cluster.controller_leader_id` or `nodes.controller_leader_id`.
- `anomalyCount` from overview Slot anomaly groups and Slot state buckets.
- `selectedNodeSlots` by checking `leader_id`, `desired_peers`, `current_peers`, and `current_voters`.
- node Slot counters from `ManagerNode.slots` when available, falling back to `slot_stats`.

No data is mutated. Refresh reloads all three read APIs.

## Components

Start with a single page implementation to avoid premature abstraction:

- `TopologyPage`
  - owns load state, refresh state, selected node filter, and error mapping.
- Small local helpers:
  - `mapErrorKind`
  - `formatNodeOption`
  - `slotMentionsNode`
  - `formatNodeList`
  - `slotAnomalyLabel`
  - `nodeSlotSummary`

If the page grows beyond the MVP, later work can extract:

- `TopologySummaryCards`
- `TopologyNodeGrid`
- `TopologySlotMatrix`

## Error And Empty States

- Initial load shows `ResourceState kind="loading"`.
- If any required API returns:
  - 403: show `ResourceState kind="forbidden"`.
  - 503: show `ResourceState kind="unavailable"`.
  - other errors: show `ResourceState kind="error"`.
- Empty node list:
  - render the summary from overview/slots if available.
  - show an empty node topology section.
- Empty Slot list:
  - show an empty Slot matrix section.
- The page must not fabricate topology data when an API returns empty arrays.

The MVP treats the three APIs as required. A partial-success mode can be added later if operators need degraded topology views during controller outages.

## Permissions

The page uses existing protected shell behavior and existing API permission checks:

- `GET /manager/overview` requires overview read permission when auth is enabled.
- `GET /manager/nodes` requires node read permission.
- `GET /manager/slots` requires Slot read permission.

No extra frontend permission gating is needed because the page has no write controls.

## Testing

Use TDD for implementation.

Frontend tests:

- `web/src/pages/topology/page.test.tsx`
  - renders summary, node topology cards, and Slot matrix from mocked APIs.
  - filters Slots by selected node.
  - maps 403 and 503 errors to `ResourceState`.
  - renders empty node/Slot states without crashing.
- `web/src/pages/page-shells.test.tsx`
  - update `/topology` expectations from placeholder copy to implemented topology copy.

Targeted verification:

```bash
cd web && bun run test -- src/pages/topology/page.test.tsx src/pages/page-shells.test.tsx
cd web && bun run build
```

## Documentation

Update:

- `web/README.md`
  - mark `/topology` as implemented with `GET /manager/overview`, `GET /manager/nodes`, and `GET /manager/slots`.
- `docs/raw/web-admin-restructure.md`
  - mark topology read-only MVP complete.
  - keep channel-level replica topology and dedicated topology API as follow-ups.

## Follow-ups

- Add `GET /manager/topology` only if frontend aggregation becomes too expensive or duplicated.
- Add channel-level replica topology after the manager API can expose channel replica relationships safely.
- Consider a graph visualization after the data model is complete; do not introduce a graph library for this MVP.
