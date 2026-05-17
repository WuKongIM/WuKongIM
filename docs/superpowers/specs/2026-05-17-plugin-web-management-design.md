# Web Plugin Management Design

## Goal

Add a first-version plugin management area to the existing web admin shell. The page lets operators inspect node-local plugin runtime state, update plugin config, restart plugins, and manage cluster-authoritative UID to plugin bindings.

## Scope

In scope:

- Add a Cluster navigation entry and route at `/cluster/plugins`.
- Load cluster nodes and select one node using the existing `NodeFilter` pattern.
- List plugins from `GET /manager/nodes/:node_id/plugins`.
- Show plugin details from `GET /manager/nodes/:node_id/plugins/:plugin_no` in a detail sheet.
- Update plugin config through `PUT /manager/nodes/:node_id/plugins/:plugin_no/config`.
- Restart one plugin through `POST /manager/nodes/:node_id/plugins/:plugin_no/restart` with confirmation.
- Query plugin bindings through `GET /manager/plugin-bindings?uid=...` or `GET /manager/plugin-bindings?plugin_no=...`.
- Add and delete bindings through `POST /manager/plugin-bindings` and `DELETE /manager/plugin-bindings`.
- Support English and Chinese copy.
- Add API, navigation, route, and page tests.

Out of scope for this first version:

- Plugin uninstall UI, even though the backend API exists.
- Plugin upload/install package management.
- Real-time streaming status updates.
- Multi-node aggregated plugin inventory.

## Information Architecture

The entry belongs in the Cluster section as `/cluster/plugins` because plugins are node-local runtime processes and protected by `cluster.plugin` permissions. The page sits alongside Nodes, Slots, Channels, Tasks, Topology, and Diagnostics.

Legacy redirects are not required because this is a new feature with no previous web route.

## Page Structure

The page follows existing web shell conventions:

- `PageContainer` and `PageHeader` for route chrome.
- `NodeFilter` to select a node, defaulting to the local node when available.
- `ResourceState` for loading, forbidden, unavailable, error, and empty states.
- `StatusBadge` for plugin runtime status.
- `DetailSheet` for plugin details.
- `ActionFormDialog` for JSON config editing and binding creation.
- `ConfirmDialog` for restart and binding deletion.

Main sections:

1. Header actions
   - Node selector.
   - Refresh button.
2. Summary cards
   - Total plugins.
   - Running plugins.
   - Failed plugins.
   - Enabled plugins.
3. Plugin inventory
   - Columns: plugin number, name/version, status, enabled flag, methods, priority, PID, last seen, actions.
   - Actions: detail, configure, restart.
4. Binding management
   - Selector mode: by UID or by plugin number.
   - Query input and search button.
   - Result table with UID, plugin number, plugin metadata when returned, warnings, actions.
   - Add binding dialog.
   - Delete binding confirmation.

## API Client Additions

Add types in `web/src/lib/manager-api.types.ts`:

- `ManagerPluginConfigField`
- `ManagerPluginConfigTemplate`
- `ManagerPlugin`
- `ManagerNodePluginsResponse`
- `ManagerPluginMutationResponse`
- `PluginBindingListParams`
- `ManagerPluginBindingWarning`
- `ManagerPluginBinding`
- `ManagerPluginBindingsResponse`
- `MutatePluginBindingInput`
- `ManagerPluginBindingMutationResponse`

Add functions in `web/src/lib/manager-api.ts`:

- `getNodePlugins(nodeId)`
- `getNodePlugin(nodeId, pluginNo)`
- `updateNodePluginConfig(nodeId, pluginNo, config)`
- `restartNodePlugin(nodeId, pluginNo)`
- `getPluginBindings(params)`
- `createPluginBinding(input)`
- `deletePluginBinding(input)`

The API client keeps raw plugin config as `Record<string, unknown>` and submits config editor JSON as an object. Secret values are already redacted by the backend; the UI must not try to recover or special-case secret content beyond preserving submitted JSON.

## Error Handling

- `403` maps to forbidden resource state.
- `501` maps to unavailable/not implemented messaging for older nodes or builds without plugin management support.
- `503` maps to service unavailable.
- Invalid config JSON is validated client-side before calling the API.
- Binding search requires exactly one selector: UID or plugin number.
- Mutations keep the dialog open and display the error when the request fails.
- Successful mutations refresh the relevant current view.

## Permissions

The backend enforces `cluster.plugin` read and write permissions. The first web version relies on server responses for authorization and maps forbidden errors consistently. If a future UI-level permission helper is introduced, plugin write buttons can be disabled before request time.

## Testing Plan

Use TDD and add failing tests before implementation:

1. API client tests
   - Builds node plugin list/detail/config/restart paths.
   - Builds binding list query paths for UID and plugin number.
   - Sends binding mutation bodies.
2. Navigation and router tests
   - `/cluster/plugins` renders under the Cluster section.
   - Navigation metadata and path label are present in English and Chinese.
3. Page tests
   - Renders node plugin inventory with status, methods, and summary counts.
   - Opens detail sheet and displays config/template/runtime metadata.
   - Validates invalid config JSON locally.
   - Submits config update and refreshes plugin data.
   - Confirms restart and refreshes plugin data.
   - Queries bindings by UID and by plugin number.
   - Adds and deletes plugin bindings.
   - Maps forbidden/unavailable errors to `ResourceState`.
4. Build verification
   - `bun run test`
   - `bun run build`
