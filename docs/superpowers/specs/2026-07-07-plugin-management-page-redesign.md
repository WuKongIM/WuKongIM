# Plugin Management Page Redesign

## Goal

Redesign the existing `/cluster/plugins` manager page so cluster operators can
quickly inspect node-local plugin health, act on one plugin, and manage UID
bindings without scanning a wide, dense table. The redesign stays on the
current manager API surface and does not add backend routes.

## Current API Boundary

The page continues to use these existing manager APIs:

- `GET /manager/nodes` for node selection.
- `GET /manager/nodes/:node_id/plugins` for the selected node plugin inventory.
- `GET /manager/nodes/:node_id/plugins/:plugin_no` for plugin details.
- `PUT /manager/nodes/:node_id/plugins/:plugin_no/config` for desired config
  updates.
- `POST /manager/nodes/:node_id/plugins/:plugin_no/restart` for restart.
- `DELETE /manager/nodes/:node_id/plugins/:plugin_no` for uninstall.
- `GET /manager/plugin-bindings?uid=...` and
  `GET /manager/plugin-bindings?plugin_no=...` for binding lookup.
- `POST /manager/plugin-bindings` and `DELETE /manager/plugin-bindings` for
  binding mutation.

Plugin inventory remains node-local because the backend lifecycle operations
target one selected node. UID bindings remain cluster-authoritative because the
current binding APIs already own that durable state.

## Chosen Approach

Use a medium redesign: reorganize the existing page, add local filters for the
currently loaded plugin list, and reduce the inventory table to the fields
operators need most often.

Alternatives considered:

- Style-only cleanup. This is safer but leaves the wide table and mixed
  inventory/binding workflow intact.
- Full plugin platform expansion with install, upgrade, package upload, or
  multi-node aggregation. This would require new backend semantics and is out of
  scope for this UI-focused request.

The medium redesign gives a clear operator workflow while preserving current
interfaces and tests.

## Page Structure

The redesigned page keeps the existing shell components:

- `PageContainer` and `PageHeader` for route chrome.
- `NodeFilter` and Refresh in the header actions.
- `ResourceState` for loading, empty, forbidden, unavailable, and error states.
- `DetailSheet` for low-frequency runtime/config/template fields.
- `ActionFormDialog` for JSON config editing and binding creation.
- `ConfirmDialog` for restart, uninstall, and binding deletion.

Main sections:

1. Header controls
   - Selected node.
   - Manual refresh.
2. Summary strip
   - Total plugins.
   - Running plugins.
   - Failed plugins.
   - Enabled plugins.
3. Plugin inventory
   - Local keyword filter across plugin number, name, version, method, and last
     error.
   - Local status filter, including an "all statuses" option.
   - Local method filter, including an "all methods" option.
   - Compact table columns: plugin, status, capabilities, last signal, actions.
   - Actions: details, configure, restart, uninstall.
4. Binding query
   - Selector: UID or plugin number.
   - Query input and search button.
   - Add binding button.
   - Result table: UID, plugin number, local plugin metadata when available,
     warnings, actions.
   - Load more remains available for plugin-number searches with cursors.

Low-frequency fields move out of the main table and into the detail sheet:
priority, PID, created/updated time, sync flags, config values, and config
template fields.

## Component Boundaries

Keep the public page export as `PluginsPage`, then split internal UI helpers to
make `web/src/pages/plugins/page.tsx` easier to reason about:

- Summary strip helpers for count presentation.
- Plugin filter helpers for deriving status/method options and filtered rows.
- Plugin inventory table component for the compact list.
- Binding query/results component for binding management.
- Detail/config/restart/uninstall dialogs stay close to the page state because
  they share mutation and refresh behavior.

These components stay in the plugin page module unless the implementation
shows a clear need for separate files. The goal is readability, not a new local
framework.

## Data Flow

On mount, the page loads nodes and selects the local node when available. When
the selected node changes, the page requests that node's plugin inventory.

Inventory filters are local and derived with `useMemo` from the loaded
`items`. Filtering must not call the backend repeatedly and must not change the
manager API contract. Refresh reuses the current selected node and resets only
network state, not the operator's local filters.

Plugin mutations call the existing node-scoped API, close their dialog on
success, and refresh the selected node inventory. Failed mutations keep their
dialog open with the error message.

Binding search stores the last successful selector parameters so add/delete can
refresh the current binding result set after mutation. Cursor-based "load more"
keeps appending rows for plugin-number searches.

## Error Handling

- `ManagerApiError` status `403` maps to the forbidden resource state.
- Status `501` and `503` map to unavailable.
- Other failures map to the generic resource error state.
- Invalid config JSON is blocked before calling the API.
- Empty binding search is blocked before calling the API.
- Add binding requires both UID and plugin number.
- Restart, uninstall, config update, add binding, and delete binding keep the
  active dialog open when the request fails.

## Performance Notes

Filtering is intentionally local and linear over the selected node's current
plugin inventory. This avoids extra backend pressure and keeps the operation
cheap for the expected plugin counts. No polling or streaming is added in this
scope; operators refresh explicitly.

## Testing Plan

Use the existing Vitest and Testing Library setup.

Focused tests should cover:

- Rendering the redesigned summary and compact plugin inventory.
- Filtering plugins by keyword, status, and method without extra API calls.
- Opening plugin detail and showing low-frequency fields there.
- Config validation and successful config refresh.
- Restart and uninstall confirmation flows.
- Binding search by UID and plugin number.
- Binding add/delete refresh behavior.
- Error-state mapping for forbidden and unavailable inventory responses.

Verification commands:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/plugins/page.test.tsx
cd web && bun run build
git diff --check
```

If `web/dist/index.html` changes only because of Vite build hash churn, restore
that generated artifact before committing source changes.
