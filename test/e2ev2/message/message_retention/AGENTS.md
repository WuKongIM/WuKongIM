# message_retention AGENTS

This scenario proves `cmd/wukongimv2` treats manager message retention as a
cluster-authoritative logical ChannelV2 compaction boundary and that enabled
physical cleanup removes retained local message-log rows without resurrecting
them after leader restart.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/message/message_retention -count=1 -timeout 2m -p=1
```

## Rules

- Keep behavior assertions black-box through public HTTP APIs.
- Physical row cleanup may be inspected through supported read-only message
  store adapters after the inspected node is stopped.
- Use a static three-node cluster with manager HTTP enabled on every node.
- Submit retention from a non-channel-leader manager node to cover RPC
  forwarding.
- Verify each node's manager message page hides retained messages before and
  after restarting the channel leader.
