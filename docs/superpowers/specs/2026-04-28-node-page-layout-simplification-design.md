# Node Page Layout Simplification Design

## Goal

Simplify the manager Nodes page layout so it feels like a compact operations page instead of a page wrapped in repeated headers, descriptions, and action areas. Keep the existing node inventory data and operations, but reduce visual noise and move secondary workflows out of the main list flow.

## Current Problems

- The page has two stacked heading systems: `PageHeader` and the `SectionCard`/`TableToolbar` title area repeat similar context and refresh actions.
- The top header includes low-value chrome such as `Scope: all nodes`, a disabled `Inspect` button, and descriptive text that does not help repeat operators.
- The scale-in plan renders as a large section below the table, causing the page to jump and making the node list harder to scan after a scale-in review.
- The useful interaction model is already clear: list rows for quick scanning, right-side detail for deeper inspection, and confirmations for risky actions. The surrounding layout should support that model rather than compete with it.

## Design

Use a compact single-surface layout:

- Keep one top line with the page title, node count, and refresh button.
- Remove the disabled top-level `Inspect` button, scope chip, repeated card description, and repeated toolbar copy.
- Replace the nested `PageHeader -> SectionCard -> TableToolbar -> table` structure with a flatter layout: compact header followed directly by one table surface.
- Keep the node table columns and row actions intact for now; this change targets page layout, not the node data model.
- Keep node detail in the existing right-side `DetailSheet`.
- Move scale-in review output into a right-side `DetailSheet`-style panel or modal-like sheet instead of rendering a large inline section below the table.

## Scale-In Interaction

Scale-in remains discoverable from the row action. When the user clicks scale-in:

1. Open a scale-in review sheet immediately with loading state.
2. Fetch the plan/status inside the sheet.
3. Show existing scale-in report content inside the sheet.
4. Keep start, advance, cancel, and refresh controls in the sheet header/footer area.
5. Keep existing confirm dialogs for start/cancel.

This keeps the main page anchored on the node list while still supporting the full scale-in workflow.

## Data Flow

No backend or API contract changes are required.

- `getNodes` still feeds the table.
- `getNode` still feeds the node detail sheet.
- `planNodeScaleIn`, `getNodeScaleInStatus`, `startNodeScaleIn`, `advanceNodeScaleIn`, and `cancelNodeScaleIn` still feed the scale-in report.
- After mutating actions, keep the existing behavior of refreshing the node list.

## Error Handling

- Node list loading/error/empty states remain in the main content area.
- Node detail errors remain inside the node detail sheet.
- Scale-in errors move into the scale-in sheet and should not replace or expand the main node list.
- Permission messaging for scale-in mutating actions remains in the scale-in sheet.

## Testing

Update `web/src/pages/nodes/page.test.tsx` to verify:

- The top-level disabled `Inspect` button and scope chip are gone.
- The node table still renders rows and row-level `Inspect`, `Drain/Resume`, and scale-in actions.
- Clicking scale-in opens a sheet/panel rather than inserting an inline `Scale-in Plan` section in the main page.
- Existing scale-in start/advance/cancel flows still call the same API functions.
- Detail sheet behavior for inspect/drain/resume still works.

## Non-Goals

- Do not remove node inventory fields from the API.
- Do not redesign the backend DTO shape.
- Do not change drain, resume, or scale-in semantics.
- Do not add charts or new summary cards in this pass.
