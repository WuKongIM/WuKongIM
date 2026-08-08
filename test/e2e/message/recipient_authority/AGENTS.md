# recipient_authority AGENTS

This scenario proves committed group SEND keeps actual ordinary membership
mutation rows unchanged while subscriber-owned conversation views remain
hydratable in a single-node cluster.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/recipient_authority -count=1
```

The 100k subscriber stress path is opt-in:

```bash
WK_E2E_100K_CONVERSATION=1 GOWORK=off go test -tags=e2e ./test/e2e/message/recipient_authority -run TestWukongIMHundredKGroupMembershipDirectoryBuildsConversations -count=1 -timeout 6m -p=1
```

## Rules

- Keep assertions black-box through public HTTP APIs and the public WKProto
  readiness probe.
- Use `test/e2e/suite` for process startup, config rendering, readiness,
  HTTP API helpers, and metrics polling.
- Validate recipient-authority conversation updates only. Online delivery and
  `RECV` assertions belong to delivery-specific scenarios.
- Keep the 100k path skipped by default. It must prove sampled subscribers are
  updated using public `/conversation/list` results, SEND adds zero ordinary
  membership mutation rows, and evidence comes from low-cardinality `/metrics`
  samples rather than direct storage inspection.
