# Go before-send Webhook example

A Go 1.22+ standard-library-only business callback for `msg.before_send`. No
WuKongIM Go SDK, database, or third-party module is required. The single
`main.go` file can also be copied elsewhere and run with `go run main.go`.

## Start

From a source checkout containing the before-send Webhook feature:

```bash
cd docs-site/examples/go-webhook
go run .
```

The endpoint is `http://127.0.0.1:8090/webhook`. Use `go run . -addr 127.0.0.1:8091`
to change the port. Only loopback IPs are accepted. Stop with Ctrl+C; accepted
requests get up to five seconds to finish.

```bash
curl -fsS http://127.0.0.1:8090/healthz
```

## Try a callback without starting WuKongIM

```bash
curl -fsS 'http://127.0.0.1:8090/webhook?event=msg.before_send' \
  -H 'Content-Type: application/json' \
  -d '{"from_uid":"alice","channel_id":"example-group","channel_type":2,"client_msg_no":"example-allow","payload":"aGVsbG8="}'
```

Expected response: `{"allow":true}`. Try these payloads in the same request:

| Decoded text | Base64 `payload` | Decision |
| --- | --- | --- |
| `hello` | `aGVsbG8=` | Allow unchanged |
| `[replace] hello` | `W3JlcGxhY2VdIGhlbGxv` | Replace with `Reviewed: hello` |
| `[reject] hello` | `W3JlamVjdF0gaGVsbG8=` | Reject with business reason 200 |

Business decisions all return HTTP 200. Rejection is
`{"allow":false,"reason_code":200}`; replacement is
`{"allow":true,"payload":"UmV2aWV3ZWQ6IGhlbGxv"}`. The payload is Base64 in both
directions because the Go fields use `[]byte`. A denied send has no committed
message ID or sequence, even when WuKongIM uses `on_error="allow"`.

## Connect WuKongIM

Add this section to your existing `wukongim.toml`, then restart the single-node
cluster or every ingress node in your test cluster with consistent settings:

```toml
[webhook.before_send]
enabled = true
http_addr = "http://127.0.0.1:8090/webhook"
timeout = "500ms"
on_timeout = "deny"
on_error = "deny"
max_in_flight = 64
```

This URL assumes native WuKongIM processes on the same host as the example.
Loopback inside another host or container refers to that host or container.
A remote callback needs a reachable, trusted proxy and appropriate source
identity; this example adds no signature or authentication header. Gateway
Token authentication is independent and stays enabled.

Through your existing client, send `hello`, `[replace] hello`, and
`[reject] hello`. Text JSON payloads such as `{"type":1,"content":"hello"}` use
the same rules on `content`. Replacement preserves the other JSON fields,
including large numeric values. Non-text JSON and opaque binary payloads pass
through unchanged. These marker rules are demonstrations, not a content filter.

For a direct WuKongIM HTTP API check from the trusted backend, create a test group:

```bash
curl -fsS http://127.0.0.1:5001/channel \
  -H 'Content-Type: application/json' \
  -d '{"channel_id":"webhook-go-example","channel_type":2,"subscribers":["alice","bob"]}'
```

Send an SDK-compatible text payload (`{"type":1,"content":"hello"}`):

```bash
curl -fsS http://127.0.0.1:5001/message/send \
  -H 'Content-Type: application/json' \
  -d '{"from_uid":"alice","channel_id":"webhook-go-example","channel_type":2,"client_msg_no":"go-allow-1","payload":"eyJ0eXBlIjoxLCJjb250ZW50IjoiaGVsbG8ifQ=="}'
```

Use `eyJ0eXBlIjoxLCJjb250ZW50IjoiW3JlcGxhY2VdIGhlbGxvIn0=` for replacement
and `eyJ0eXBlIjoxLCJjb250ZW50IjoiW3JlamVjdF0gaGVsbG8ifQ==` for rejection, with
a distinct `client_msg_no` for each new message. A retry of the same message
must retain its original key and body. Inspect the JSON `reason`: 1 means
success, 200 is this example's rejection, and 15 indicates a callback/system
failure with the configuration above. HTTP 200 alone does not establish send
success. The example logs only `decision=allow`, `replace`, or `reject`.

```bash
curl -fsS http://127.0.0.1:5001/channel/messagesync \
  -H 'Content-Type: application/json' \
  -d '{"login_uid":"bob","channel_id":"webhook-go-example","channel_type":2,"limit":10}'
```

Committed history contains the allowed message and the rewritten message;
there is no row for the rejected message.

## Customize and check

Edit `evaluate` in `main.go` to implement your own business decision. Its current
rules are pure: the same input and rules produce the same decision without a
cache, external calls, or side effects. It does not provide durable exactly-once
processing. If you add business writes, implement durable idempotency using the
sender, source channel, and a nonempty `client_msg_no`; a callback allowance does
not guarantee the message will subsequently commit.

The HTTP envelope is capped at 64 KiB and decoded Payload at 32767 bytes. A
rewrite exceeding the Payload limit rejects with business reason 200. At most
64 request bodies are handled concurrently per example process; excess requests
receive HTTP 503, which WuKongIM handles according to `on_error`. Request/header
and shutdown deadlines are bounded. Per-request decision logging is intended for
manual development, not production throughput qualification.

```bash
go test . -count=1
go test -race . -count=1
```

The repository also builds this example as a separate process and verifies its
decisions against real WuKongIM sends and committed history:

```bash
# From the repository root:
GOWORK=off go test -tags=e2e ./test/e2e/message/webhook \
  -run '^TestGoBeforeSendWebhookExample$' -count=1 -timeout 2m -p=1 -v
```
