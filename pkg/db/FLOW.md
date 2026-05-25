# pkg/db Flow

`pkg/db` is the root package for node-local storage. It owns shared errors,
options, and the root `NodeStore` handle.

Current flow:

1. Build `NodeStoreOptions` with `DefaultNodeStoreOptions` or explicit paths.
2. Open the root handle with `OpenNodeStore`.
3. Later tasks attach message and metadata domain stores to this root.
4. Close the root handle during application shutdown.

Pebble-specific code must stay under `pkg/db/internal/*` and must not leak into
callers.
