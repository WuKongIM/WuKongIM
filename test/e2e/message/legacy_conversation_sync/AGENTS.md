# legacy_conversation_sync AGENTS

This scenario proves the deprecated v2.2 `/conversation/sync` compatibility
surface projects committed person and group messages into every member's
recent-conversation view in real single-node and multi-node clusters.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/legacy_conversation_sync -count=1 -timeout 3m -p=1
```

## Rules

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
