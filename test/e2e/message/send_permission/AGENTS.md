# send_permission AGENTS

This scenario proves `cmd/wukongim` enforces migrated legacy send-permission
decisions in a single-node cluster through public HTTP APIs.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/send_permission -count=1
```

## Rules

- Keep assertions black-box through public channel-management and `/message/send`
  HTTP APIs.
- Use `test/e2e/suite` for process startup, config rendering, readiness, and
  HTTP API helpers.
- Cover rejection reason codes for channel existence, membership, denylist,
  allowlist, and sender send-ban behavior.
