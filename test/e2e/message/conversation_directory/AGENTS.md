# conversation_directory AGENTS

This scenario proves membership-backed ordinary conversation directory behavior
through a real single-node cluster.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_directory -count=1 -timeout 2m
```

## Rules

- Keep assertions black-box through public WKProto, channel-management,
  conversation mutation, `/conversation/list`, and `/metrics` entrypoints.
- Do not inspect Pebble or import product internals.
- Use candidate-limit pagination and opaque cursors exactly as a client would.
- Keep CMD directory behavior in `message/cmd_sync`; this scenario owns the
  ordinary directory, badge, activation, hide, remove, and rejoin behavior.
