# message/webhook AGENTS

This scenario owns black-box webhook coverage for `cmd/wukongim`.

## Purpose

Prove a real single-node cluster can deliver node-local webhook callbacks for
committed SEND side effects through the public WKProto gateway and an external
HTTP endpoint.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/webhook -count=1 -timeout 2m -p=1
```

## Rules

- Keep assertions black-box through real `cmd/wukongim`, real WKProto SEND,
  and HTTP requests observed by the test webhook endpoint.
- Do not import `internal/app`, `internal/usecase`, or storage internals.
- Keep webhook waits bounded and include node diagnostics plus captured webhook
  requests on failure.
