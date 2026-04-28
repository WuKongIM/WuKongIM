# Slot and Connection Page Layout Simplification Design

## Goal

Simplify the manager Slots and Connections pages so operators reach the inventory tables and primary actions faster. Match the compact table-first layout already used by the Nodes, Channels, and Messages pages while preserving all existing data, permissions, table columns, detail sheets, and slot operation flows.

## Current Problems

- Both pages repeat context across `PageHeader`, scope chips, summary cards, `SectionCard`, and `TableToolbar`.
- The summary cards consume the first screen but duplicate information already visible in the inventory tables.
- The inventory card titles and toolbar descriptions add another layer before the operator reaches the rows.
- The Connections page has no write actions, so its useful workflow is simple: refresh, scan sessions, inspect one connection.
- The Slots page needs write actions, but those actions can live in the compact page header instead of a large header card.

## Design

Use the same compact page structure for both pages:

- Render one compact top row with page title, total count, and page-level actions.
- Remove scope chips, page descriptions, summary cards, inventory `SectionCard` chrome, and `TableToolbar` chrome.
- Render each inventory table in one flat card-like surface.
- Keep loading, error, and empty states in the main content area.
- Keep all table columns and row actions unchanged.
- Keep all detail sheets and dialogs unchanged.

## Slots Page

The Slots page keeps its operational actions but removes repeated chrome:

- Header:
  - `Slots`
  - `Total: n` or `Total: pending`
  - `Refresh`
  - `Add slot`
  - `Rebalance slots`
- Inventory:
  - One flat table surface containing the existing slot table.
  - Existing columns remain: slot, quorum, sync, desired peer set, current peer set, leader, actions.
  - Existing Inspect action still opens the slot detail sheet.
- Slot operations:
  - Add slot, rebalance, remove slot, transfer leader, and recover slot keep existing permission checks and dialog flows.
  - Slot detail footer actions remain in the detail sheet.
- Rebalance plan:
  - If a rebalance plan exists, render it below the slot table as a lightweight result panel.
  - Keep each plan item and empty state, but remove the large `SectionCard` title/description chrome.

## Connections Page

The Connections page becomes a direct local connection inventory:

- Header:
  - `Connections`
  - `Total: n` or `Total: pending`
  - `Refresh`
- Inventory:
  - One flat table surface containing the existing connection table.
  - Existing columns remain: session, UID, device ID, device, listener, state, connected at, actions.
  - Existing Inspect action still opens the connection detail sheet.
- Detail:
  - Keep the connection detail fields and error/retry behavior unchanged.

## Data Flow

No backend or API contract changes are required.

- `getSlots()` still loads and refreshes the slot inventory.
- `getSlot(slotID)` still loads slot detail.
- `addSlot()`, `removeSlot()`, `transferSlotLeader()`, `recoverSlot()`, and `rebalanceSlots()` keep their current request and refresh behavior.
- `getConnections()` still loads and refreshes the local connection inventory.
- `getConnection(sessionID)` still loads connection detail.

## Error Handling

- Initial loading, forbidden, unavailable, general error, and empty states stay in each page's main content.
- Slot operation dialog errors remain inside their existing dialogs.
- Slot and connection detail loading/errors remain inside the existing `DetailSheet`.
- Rebalance conflict feedback stays in the existing rebalance confirmation flow.

## Testing

Update the existing page tests to verify:

- Slots:
  - Compact header renders total count and the three page-level actions.
  - Old scope chip, page description, summary cards, inventory descriptions, and toolbar descriptions do not render.
  - Existing slot write actions, detail, transfer, recover, rebalance, remove, unavailable, conflict, and translated validation tests still pass.
- Connections:
  - Compact header renders total count and refresh.
  - Old scope chip, page description, summary cards, inventory descriptions, and toolbar descriptions do not render.
  - Existing connection rows, refresh, detail, unavailable, and empty-state tests still pass.

## Non-Goals

- Do not change manager API shapes.
- Do not add filters or pagination.
- Do not remove table columns.
- Do not redesign slot or connection detail sheets.
- Do not change slot operation permission checks.
- Do not change i18n messages unless TypeScript or build verification requires it.
