# Merge failure inventory before resolution

Scope: integrate exact main into the existing MQTT PR, preserving both parents.
Use existing public process/E2E seams and existing protocol/catalog/lifecycle
contracts; add no unit tests after implementation and introduce no new seam.

1. RPC or Slot command additions reuse the same number for different operations.
2. Product composition loses MQTT startup/restore/stop ownership or main diagnostics.
3. Controller Raft publication locking or main transport observations are dropped.
4. Quorum funding/unknown-outcome rules lose main batching or cancellation bounds.
5. WKProto harness credentials lose main reconnect or terminal-receipt behavior.
6. FLOW/index and Changelog lose either parent's current facts/notes.
7. A merged candidate compiles but breaks ordinary MQTT interop or partition recovery.

Validation: existing focused catalog, composition, transport, controller, storage
and E2E harness tests; ordinary product MQTT/Will and live three-node partition
E2E; named flow-doc-contracts. Preserve exact commands/results and report limits.
