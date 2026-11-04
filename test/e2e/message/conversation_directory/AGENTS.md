# conversation_directory AGENTS

This scenario proves membership-backed ordinary conversation directory behavior
through real single-node and three-node clusters.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_directory -count=1 -timeout 2m
```

## Rules

- Keep assertions black-box through public WKProto, channel-management,
  conversation mutation, `/conversation/list`, `/conversation/sync`, and `/metrics` entrypoints.
- Do not inspect Pebble or import product internals.
- Use candidate-limit pagination and opaque cursors exactly as a client would.
- Keep CMD directory behavior in `message/cmd_sync`; this scenario owns the
  ordinary directory, badge, activation, hide, remove, and rejoin behavior.

`TestUnreadActionsRefreshConversationActivation` uses 256 Hash Slots in both
topologies and a remote UID Slot Leader in the three-node case. Prove set/clear
refresh activation and ordering in both conversation APIs even when the read position is unchanged; keep
monotonic badge floors, hidden history, and absent/removed membership behavior.
Use an existing Channel with hidden history for the no-visible-message case.
`WK_E2E_UNREAD_ACTIVATION_REPORT_DIR` retains per-topology JSON with public
responses and verdicts after process cleanup; the default is a temporary directory.
