# wukongimv2 conversation projection AGENTS

This scenario proves `cmd/wukongimv2` can project real group messages into
UID-owned conversation rows through a single-node cluster.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/wukongimv2_conversation_projection -count=1
```

The 100k subscriber stress path is opt-in:

```bash
WK_E2E_100K_CONVERSATION=1 GOWORK=off go test -tags=e2e ./test/e2e/message/wukongimv2_conversation_projection -run TestWukongIMV2HundredKGroupConversationProjectionStaysSparse -count=1 -timeout 6m
```

## Rules

- Keep assertions black-box through public HTTP APIs and the public WKProto
  readiness probe.
- Build `cmd/wukongimv2` inside the test; do not use the default e2e binary
  cache because it targets `cmd/wukongim`.
- Validate conversation projection only. Online delivery and `RECV` assertions
  belong to delivery-specific scenarios.
- Keep the 100k path skipped by default. It must prove sparse projection using
  public `/conversation/list` results and low-cardinality `/metrics` samples,
  not direct storage inspection.
