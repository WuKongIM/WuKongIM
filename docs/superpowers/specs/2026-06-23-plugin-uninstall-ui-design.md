# Plugin Uninstall UI Design

## Goal

Complete the manager plugin lifecycle surface by adding a focused uninstall
action to the existing `/cluster/plugins` page.

## Scope

In scope:

- Add a web manager API client function for
  `DELETE /manager/nodes/:node_id/plugins/:plugin_no`.
- Add an Uninstall action to each plugin inventory row.
- Confirm uninstall before calling the backend.
- Refresh the selected node plugin inventory after a successful uninstall.
- Keep the confirmation dialog open and show the backend error when uninstall
  fails.
- Cover the API client path and page interaction with tests.

Out of scope:

- Plugin package upload or installation.
- Multi-node aggregated plugin inventory.
- A schema-driven config form.
- Real-time plugin process status streaming.

## User Experience

The page already exposes details, config update, restart, and binding
management. Uninstall belongs in the same row action group because it targets
one node-local plugin process. The action uses the existing `ConfirmDialog`
pattern and warns that uninstall disables and removes the plugin from the
selected node.

Successful uninstall closes the dialog and refreshes the inventory. If the
backend rejects the request, the dialog remains open with the error message so
the operator can retry or cancel without losing context.

## API Contract

The web API client exports:

```ts
deleteNodePlugin(nodeId: number, pluginNo: string): Promise<void>
```

It calls:

```text
DELETE /manager/nodes/:node_id/plugins/:plugin_no
```

`pluginNo` is URL encoded, matching detail, config, and restart calls.

## Error Handling

The UI does not pre-guess uninstall eligibility. It relies on the manager
backend for permission and lifecycle validation. Any thrown `ManagerApiError`
or generic error is rendered in the uninstall confirmation dialog.

## Testing

Use TDD:

- Extend `web/src/lib/manager-api.test.ts` to prove the API client sends the
  delete request to the correct URL.
- Extend `web/src/pages/plugins/page.test.tsx` to prove the uninstall action
  opens a confirmation dialog, calls the API, and refreshes the inventory.
- Verify with focused web tests and a production build.
