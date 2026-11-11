# message/webhook AGENTS

This scenario owns black-box webhook coverage for `cmd/wukongim`.

## Purpose

Prove single-node cluster post-commit notifications and three-node synchronous
msg.before_send admission through real WKProto, Product HTTP, committed history,
and an external HTTP endpoint. Cover payload mutation, business rejection,
independent timeout/error policies, transient sends, and one callback per ingress
attempt across authority forwarding. The authenticated fault test also checks
Token rejection, slow/unavailable/malformed/redirecting callbacks, per-node
overload isolation and recovery, callback counts, and public metric deltas.
`TestGoBeforeSendWebhookExample` builds the published Go example, owns its
separate process, and checks Product HTTP decisions and committed history in a
single-node cluster. Gateway Token auth stays enabled; explicit denial is also
checked with `on_error=allow`.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/webhook -count=1 -timeout 2m -p=1
```

## Rules

- Keep assertions black-box through real `cmd/wukongim`, real WKProto SEND,
  and HTTP requests observed by the test webhook endpoint.
- Do not import `internal/app`, `internal/usecase`, or storage internals.
- Keep webhook waits bounded and include node diagnostics plus captured webhook
  requests on failure. Authenticated receive failures preserve callback counts,
  the warm HTTP receipt, client queue state, public receiver online status and
  at most 100 nonzero delivery/presence/webhook/gateway/channelappend metric
  samples per node, including omitted-sample counts. Public diagnostic reads
  share a separate five-second bound after the original receive fails; the
  five-second receive assertion remains unchanged.

- The notification and admission tests explicitly disable Gateway Token
  authentication to isolate callback semantics. `TestBeforeSendWebhookAuthenticatedFaults`
  enables Token authentication on all three nodes, provisions test credentials
  through Product HTTP, and checks both valid cross-node sessions and invalid Tokens.
- The fault test uses 256 hash slots, two Slot replicas, a three-second callback
  timeout, and two in-flight calls per node. Node-specific failure policies are
  deliberate test fixtures; deployment configurations must remain consistent.
- Resource snapshots and a finite saturation burst are diagnostic evidence,
  not production capacity or leak qualification. Report absent CPU metrics as
  unavailable, never zero. Close test callbacks and all node processes on exit.
