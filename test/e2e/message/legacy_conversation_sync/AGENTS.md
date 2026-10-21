# legacy_conversation_sync AGENTS

This scenario proves the deprecated v2.2 `/conversation/sync` compatibility
surface projects committed person and group messages into every member's
recent-conversation view in real single-node and multi-node clusters with 256
Hash Slots. Oversized recent-message requests must retain every selected
conversation by reducing the per-conversation recent-message limit.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/legacy_conversation_sync -count=1 -timeout 3m -p=1
```

Set `WK_E2E_LEGACY_SYNC_REPORT_DIR=/absolute/artifact-directory` to retain one
budget-regression JSON report per topology after test cleanup. Otherwise each
report is written inside the temporary node workspace. Reports retain the exact
test-generated request, expected messages, public response and success/failure.

## Rules

- Explicitly disable Gateway Token authentication on every fixture node for
  the existing tokenless readiness and WKProto clients; this scenario tests
  conversation projection, not authentication.

- Keep assertions black-box through WKProto, public channel-management HTTP,
  legacy `/conversation/sync`, and public Manager HTTP only.
- Keep recipients offline so this scenario isolates durable conversation sync
  from delivery and `RECV/RECVACK` behavior.
- Exercise full sync followed by per-Channel cursor sync in one flow; do not
  compare response versions because legacy versions can be equal within one
  timestamp second.
- Multi-node coverage must enter through node 1 while the tested Channel
  Leader and at least one recipient UID Slot Leader are on another node.
- Keep the multi-node fixture on the default three Channel replicas so the
  scenario covers the committed-HW visibility needed by legacy sync.
- Reuse the group flow's sender and offline members for a second group. First
  prove both conversations are visible with `msg_count=1`, then send one
  `msg_count=10000` request for each of the sender and an offline recipient.
  Require both rows, exact latest-message identity/payload, badges and cursor
  behavior; do not retry the oversized request or create thousands of messages.
