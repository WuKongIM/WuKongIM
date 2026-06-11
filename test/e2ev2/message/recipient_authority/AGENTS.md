# recipient_authority AGENTS

This scenario proves `cmd/wukongimv2` can route committed group messages through
recipient UID authority and update subscriber-owned recent conversations in a
single-node cluster.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/message/recipient_authority -count=1
```

The 100k subscriber stress path is opt-in:

```bash
WK_E2E_100K_CONVERSATION=1 GOWORK=off go test -tags=e2e ./test/e2ev2/message/recipient_authority -run TestWukongIMV2HundredKGroupRecipientAuthorityUpdatesSubscribers -count=1 -timeout 6m
```

## Rules

- Keep assertions black-box through public HTTP APIs and the public WKProto
  readiness probe.
- Use `test/e2ev2/suite` for process startup, config rendering, readiness,
  HTTP API helpers, and metrics polling.
- Validate recipient-authority conversation updates only. Online delivery and
  `RECV` assertions belong to delivery-specific scenarios.
- Keep the 100k path skipped by default. It must prove sampled subscribers are
  updated using public `/conversation/list` results and low-cardinality
  `/metrics` samples, not direct storage inspection.
