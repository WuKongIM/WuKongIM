---
title: Network Observability Page Redesign
date: 2026-05-06
status: approved
---

# Network Observability Page Redesign

## Problem Statement

The current network observability page (`/network`) displays 12+ sections with extensive charts and tables, making it overwhelming and difficult to quickly assess network health. Users report that the interface is too complex and not intuitive for their primary use case: monitoring node-to-node communication status.

## User Requirements

**Primary Goal**: Quickly understand whether communication between nodes is healthy (latency, connection pools, success rates).

**Key User Needs**:
- At-a-glance view of network health across all nodes
- Ability to filter/drill down to specific nodes
- Clear visualization of key metrics with trends
- Quick identification of anomalies or errors

## Design Approach

Redesign the page using a **cloud monitoring dashboard pattern** (similar to Alibaba Cloud/Tencent Cloud monitoring), featuring:
- Top filter bar for node selection and time range
- Grid of metric cards, each containing one focused chart
- Consistent card layout with primary metric + mini trend chart
- Optional detail drawer for deep-dive analysis

---

## Page Structure

### 1. Top Filter Bar

**Components**:
- **Node Selector**: Multi-select dropdown
  - "All Nodes" option (default)
  - Individual node selection (by node_id or name)
  - Multi-node selection support
- **Time Range Selector**: Dropdown with preset options
  - Last 1 minute (default)
  - Last 5 minutes
  - Last 15 minutes
- **Refresh Controls**:
  - Manual refresh button
  - Auto-refresh toggle (optional, 30s interval)
- **Scope Badges**: Display current view context
  - Local node ID
  - Controller leader ID
  - Last updated timestamp

**Behavior**:
- Changing node selection re-aggregates all card data
- Time range affects historical trend charts
- Filter state persists in URL query params for sharing

### 2. Metric Cards Grid

**Layout**: Responsive grid (3 columns on large screens, 2 on medium, 1 on small)

**Card Structure** (consistent across all cards):
```
┌─────────────────────────────┐
│ 📊 Card Title               │
│                             │
│     Primary Metric          │
│     (large number)          │
│                             │
│  ┌─────────────────────┐   │
│  │  Mini Chart         │   │
│  └─────────────────────┘   │
│                             │
│  Label 1  Label 2  Label 3  │
└─────────────────────────────┘
```

#### Card 1: Node Health Status

**Purpose**: Show cluster node health distribution at a glance

**Content**:
- **Primary Metric**: `{alive_nodes} / {total_nodes}` (e.g., "8 / 10")
- **Mini Chart**: Donut chart showing alive/suspect/dead/draining distribution
- **Bottom Labels**: 
  - 🟢 Alive: {count}
  - 🟡 Suspect: {count}
  - 🔴 Dead: {count}
  - 🔵 Draining: {count}

**Data Source**: `summary.headline.{alive_nodes, suspect_nodes, dead_nodes, draining_nodes}`

**Filter Behavior**: 
- All Nodes: Show all node health
- Specific Node(s): Show only selected nodes' health

#### Card 2: Connection Pool Status

**Purpose**: Monitor connection pool utilization

**Content**:
- **Primary Metric**: `{active} / {idle}` (e.g., "45 / 15")
- **Mini Chart**: Stacked horizontal bar (cluster pool + data plane pool)
- **Bottom Labels**:
  - Cluster: {active}/{idle}
  - Data Plane: {active}/{idle}
  - Total: {active+idle}

**Data Source**: 
- All Nodes: `summary.headline.{pool_active, pool_idle}`
- Specific Node(s): Aggregate from `summary.peers[selected].pools`

**Alert Condition**: Highlight if active > 80% of total

#### Card 3: RPC Call Latency

**Purpose**: Monitor RPC performance across nodes

**Content**:
- **Primary Metric**: P95 latency in ms (e.g., "45 ms")
- **Mini Chart**: Line chart showing P95 trend over time window
- **Bottom Labels**:
  - P50: {value} ms
  - P95: {value} ms
  - P99: {value} ms

**Data Source**:
- All Nodes: Aggregate P95 from `summary.services[]`
- Specific Node(s): Filter `summary.peers[selected].rpc.p95_ms`

**Alert Condition**: Highlight if P95 > 100ms

#### Card 4: RPC Success Rate

**Purpose**: Show RPC call reliability

**Content**:
- **Primary Metric**: Success rate percentage (e.g., "98.5%")
- **Mini Chart**: Stacked area chart (success/errors/expected_timeouts)
- **Bottom Labels**:
  - Total Calls: {count}
  - Success: {count}
  - Failures: {count}

**Data Source**: Calculate from `summary.services[]` or `summary.peers[].rpc`

**Alert Condition**: Highlight if success rate < 95%

#### Card 5: Network Traffic

**Purpose**: Monitor bandwidth utilization

**Content**:
- **Primary Metric**: Current rate (e.g., "1.2 MB/s")
- **Mini Chart**: Dual-line area chart (TX orange, RX cyan)
- **Bottom Labels**:
  - TX: {bytes} ({bps} bps)
  - RX: {bytes} ({bps} bps)
  - Total: {tx+rx}

**Data Source**: `summary.traffic` and `summary.history.traffic[]`

**Filter Behavior**: All nodes shows aggregate, specific nodes show per-node traffic

#### Card 6: Network Errors

**Purpose**: Identify error patterns quickly

**Content**:
- **Primary Metric**: Total errors in last 1m (e.g., "12")
- **Mini Chart**: Stacked bar chart (dial/queue/timeout breakdown)
- **Bottom Labels**:
  - 🔴 Dial Errors: {count}
  - 🟡 Queue Full: {count}
  - 🟠 Timeouts: {count}

**Data Source**: `summary.headline.{dial_errors_1m, queue_full_1m, timeouts_1m}`

**Alert Condition**: Highlight if total errors > 10

#### Card 7: Message Type Distribution

**Purpose**: Understand traffic composition

**Content**:
- **Primary Metric**: Most active message type name
- **Mini Chart**: Horizontal bar chart (Top 5 message types by bytes)
- **Bottom Labels**:
  - Total Types: {count}
  - Total Messages: {count}

**Data Source**: `summary.traffic.by_message_type[]`

**Filter Behavior**: Filter by selected nodes if applicable

#### Card 8: Recent Events

**Purpose**: Quick access to latest network observations

**Content**:
- **Primary Metric**: Event count in last 1m
- **Event List**: Latest 3 events with:
  - Severity badge
  - Event kind
  - Target node
  - Timestamp
- **Bottom Link**: "View All Events" (opens detail drawer)

**Data Source**: `summary.events[]`

**Filter Behavior**: Filter events by selected nodes

### 3. Detail Drawer (Optional Enhancement)

**Trigger**: Click on any metric card

**Behavior**: Slide-in drawer from right side (overlay on mobile, side panel on desktop)

**Content**:
- **Header**: Card title + close button
- **Full Chart**: Larger version of the mini chart with more detail
- **Data Table**: Detailed breakdown of the metric
- **Related Config**: Relevant configuration values
- **Actions**: Export data, view raw JSON

**Example - RPC Latency Detail Drawer**:
- Full-size line chart with P50/P95/P99 lines
- Table of all RPC services with latency breakdown
- Configuration: RPC timeout settings, pool sizes

---

## Data Aggregation Logic

### Node Filter Modes

#### All Nodes (Default)
- Use `summary.headline.*` for aggregate metrics
- Use `summary.history.*` for trends
- Show all peers in relevant cards

#### Single Node Selected
- Filter `summary.peers[]` to selected node
- Use peer-specific metrics: `peers[x].pools`, `peers[x].rpc`, `peers[x].errors`
- Show only data related to that node

#### Multiple Nodes Selected
- Aggregate metrics from selected `summary.peers[]`
- Sum counters (calls, errors, bytes)
- Average rates and percentiles
- Union of events from selected nodes

### Time Range Handling

**Current Implementation**: API returns last 1 minute data with history points

**Proposed Enhancement** (optional, requires backend changes):
- Add `?window=1m|5m|15m` query param to `/manager/network/summary`
- Backend adjusts history window and aggregation accordingly
- If not implemented: Frontend uses existing 1m data for all time ranges

---

## Component Architecture

### File Structure

```
web/src/pages/network/
├── page.tsx                    # Main page component
├── components/
│   ├── filter-bar.tsx          # Top filter controls
│   ├── metric-card.tsx         # Reusable card component
│   ├── node-health-card.tsx    # Card 1
│   ├── connection-pool-card.tsx # Card 2
│   ├── rpc-latency-card.tsx    # Card 3
│   ├── rpc-success-card.tsx    # Card 4
│   ├── traffic-card.tsx        # Card 5
│   ├── errors-card.tsx         # Card 6
│   ├── message-types-card.tsx  # Card 7
│   ├── events-card.tsx         # Card 8
│   └── detail-drawer.tsx       # Optional detail view
├── hooks/
│   ├── use-network-data.ts     # Data fetching hook
│   └── use-node-filter.ts      # Filter state management
└── utils/
    ├── aggregation.ts          # Data aggregation logic
    └── formatters.ts           # Number/time formatters
```

### Component Hierarchy

```
NetworkPage
├── FilterBar
│   ├── NodeSelector (multi-select)
│   ├── TimeRangeSelector
│   └── RefreshButton
├── MetricCardsGrid
│   ├── NodeHealthCard
│   ├── ConnectionPoolCard
│   ├── RpcLatencyCard
│   ├── RpcSuccessCard
│   ├── TrafficCard
│   ├── ErrorsCard
│   ├── MessageTypesCard
│   └── EventsCard
└── DetailDrawer (conditional)
```

### Key Abstractions

#### MetricCard Component

**Props**:
```typescript
interface MetricCardProps {
  title: string
  primaryMetric: string | number
  chart: ReactNode
  labels?: { label: string; value: string | number }[]
  alertLevel?: 'none' | 'warning' | 'danger'
  onClick?: () => void
}
```

**Responsibilities**:
- Consistent card styling and layout
- Alert state visual treatment (border color, background)
- Click handler for detail drawer
- Loading and error states

#### useNodeFilter Hook

**State**:
```typescript
interface NodeFilterState {
  selectedNodes: number[] // node_ids, empty = all nodes
  timeRange: '1m' | '5m' | '15m'
  autoRefresh: boolean
}
```

**Methods**:
- `setSelectedNodes(nodeIds: number[])`
- `setTimeRange(range: string)`
- `toggleAutoRefresh()`
- `isNodeSelected(nodeId: number): boolean`

#### useNetworkData Hook

**Responsibilities**:
- Fetch data from `/manager/network/summary`
- Apply node filtering to raw data
- Aggregate metrics based on filter state
- Handle loading, error, and refresh states

**Returns**:
```typescript
interface NetworkData {
  summary: ManagerNetworkSummaryResponse | null
  filteredData: FilteredNetworkMetrics
  loading: boolean
  error: Error | null
  refresh: () => Promise<void>
}
```

---

## Visual Design

### Card Styling

**Base Card**:
- Border: 1px solid border color
- Border radius: 12px
- Padding: 16px
- Background: card background
- Shadow: subtle on hover

**Alert States**:
- Warning: Yellow/amber left border (4px), light yellow background tint
- Danger: Red left border (4px), light red background tint

**Typography**:
- Title: 12px uppercase, tracking wide, muted foreground
- Primary Metric: 32px semibold, foreground
- Labels: 11px, muted foreground

### Chart Styling

**Consistent Colors** (reuse existing palette):
- Alive/Success: Green (`hsl(142 70% 45%)`)
- Suspect/Warning: Amber (`hsl(38 92% 50%)`)
- Dead/Error: Red (`hsl(0 72% 51%)`)
- Draining/Info: Blue (`hsl(217 91% 60%)`)
- TX: Orange (`hsl(24 95% 53%)`)
- RX: Cyan (`hsl(173 80% 40%)`)

**Chart Dimensions**:
- Mini charts: 120-160px height
- Full charts (detail drawer): 300-400px height

---

## Implementation Notes

### Reusable Components

Leverage existing components from the codebase:
- `ChartContainer`, `ChartTooltip` from `@/components/ui/chart`
- `StatusBadge` from `@/components/manager/status-badge`
- `ResourceState` for loading/error states
- `PageContainer`, `PageHeader` from `@/components/shell`

### Data Transformation

Create utility functions in `utils/aggregation.ts`:
- `aggregateNodeMetrics(peers: ManagerNetworkPeer[], nodeIds: number[])`
- `calculateSuccessRate(service: ManagerNetworkRPCService)`
- `aggregateTraffic(traffic: ManagerNetworkTraffic, nodeIds: number[])`
- `filterEventsByNodes(events: ManagerNetworkEvent[], nodeIds: number[])`

### Performance Considerations

- Memoize chart data transformations with `useMemo`
- Debounce filter changes (300ms) to avoid excessive re-renders
- Use `React.memo` for individual metric cards
- Lazy load detail drawer component

### Accessibility

- All charts use `accessibilityLayer` prop
- Keyboard navigation for filter controls
- ARIA labels for metric cards
- Focus management for detail drawer

---

## Migration Strategy

### Phase 1: New Page Implementation
1. Create new component structure in `pages/network/components/`
2. Implement filter bar and metric cards
3. Test with existing API endpoint
4. Keep old page accessible via feature flag or separate route

### Phase 2: Refinement
1. Add detail drawer functionality
2. Implement URL state persistence
3. Add auto-refresh capability
4. Polish animations and transitions

### Phase 3: Cutover
1. A/B test with users
2. Gather feedback
3. Make new design default
4. Remove old implementation

### Rollback Plan
- Keep old `page.tsx` as `page.legacy.tsx`
- Feature flag: `NETWORK_PAGE_V2_ENABLED`
- Can switch back via environment variable

---

## API Requirements

### Current API: `/manager/network/summary`

**Sufficient for MVP**: Yes, existing endpoint provides all needed data

**Optional Enhancements** (backend changes):
1. Add `?window=1m|5m|15m` query parameter for time range
2. Add `?nodes=1,2,3` query parameter for server-side filtering
3. Return pre-aggregated metrics for selected nodes

**Frontend Workaround**: If backend enhancements not available, frontend will:
- Use existing 1m window for all time ranges
- Perform client-side aggregation for node filtering

---

## Testing Strategy

### Unit Tests
- Filter state management (`use-node-filter.test.ts`)
- Data aggregation logic (`aggregation.test.ts`)
- Individual card components

### Integration Tests
- Filter changes update all cards correctly
- API error handling
- Loading states

### Visual Regression Tests
- Card layouts at different screen sizes
- Alert state styling
- Chart rendering

### Manual Testing Checklist
- [ ] All nodes view shows correct aggregate data
- [ ] Single node selection filters data correctly
- [ ] Multi-node selection aggregates properly
- [ ] Time range selector affects trend charts
- [ ] Refresh button updates data
- [ ] Auto-refresh works (if implemented)
- [ ] Alert states trigger at correct thresholds
- [ ] Detail drawer opens and displays correct data
- [ ] Responsive layout works on mobile/tablet/desktop
- [ ] Keyboard navigation works
- [ ] Screen reader announces card content

---

## Success Metrics

### User Experience
- Time to identify unhealthy node: < 5 seconds (vs ~30s with old design)
- User satisfaction score: > 4/5
- Reduced support tickets about "network page too complex"

### Technical
- Page load time: < 2s
- Time to interactive: < 3s
- Lighthouse performance score: > 90

### Adoption
- 80% of users prefer new design in A/B test
- No increase in error rates or support requests

---

## Future Enhancements

### Phase 2 Features (Post-MVP)
1. **Custom Card Arrangement**: Drag-and-drop to reorder cards
2. **Card Visibility Toggle**: Hide/show specific cards
3. **Threshold Configuration**: User-defined alert thresholds
4. **Export Functionality**: Download metrics as CSV/JSON
5. **Comparison Mode**: Compare two time periods side-by-side
6. **Real-time Updates**: WebSocket for live data streaming

### Advanced Features
1. **Anomaly Detection**: ML-based anomaly highlighting
2. **Predictive Alerts**: Forecast potential issues
3. **Custom Dashboards**: Save multiple dashboard configurations
4. **Correlation Analysis**: Show related metrics when one alerts

---

## Open Questions

1. **Auto-refresh interval**: 30s, 60s, or user-configurable?
   - **Decision**: Start with 30s, make configurable in Phase 2

2. **Detail drawer vs modal**: Which interaction pattern?
   - **Decision**: Drawer on desktop, modal on mobile

3. **Historical data retention**: How far back should time range go?
   - **Decision**: Start with 1m/5m/15m, expand based on backend capability

4. **Node naming**: Use node_id, name, or both in selector?
   - **Decision**: Show name if available, fallback to "node-{id}"

---

## Conclusion

This redesign transforms the network observability page from an overwhelming 12-section dashboard into a focused, scannable grid of 8 metric cards. The cloud monitoring pattern provides:

- **Clarity**: Each card has one clear purpose
- **Flexibility**: Node filtering enables both overview and drill-down
- **Efficiency**: Key metrics visible at a glance
- **Scalability**: Card-based design easily accommodates future metrics

The implementation leverages existing components and patterns from the codebase, ensuring consistency and maintainability.
