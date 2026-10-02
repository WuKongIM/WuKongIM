# MQTT first-message example

This Node.js >=20.11 example pins MQTT.js 5.16.0. It needs a WuKongIM
development candidate containing the default-off MQTT 5 TCP implementation.
Do not assume current released server packages/images include it. Full Linux,
fault and load qualification remains outstanding.

Start a single-node cluster from the repository root:

```bash
go build -o ./bin/wukongim-mqtt ./cmd/wukongim
mkdir -p ./tmp/mqtt-quickstart
cp wukongim.toml.example ./tmp/mqtt-quickstart/wukongim.toml
WK_NODE_DATA_DIR=./tmp/mqtt-quickstart/data \
WK_MQTT_ENABLE=true \
WK_MQTT_LISTEN_ADDR=127.0.0.1:1883 \
WK_GATEWAY_TOKEN_AUTH_ON=true \
WK_GATEWAY_LISTENERS='[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"gnet","protocol":"wkproto"}]' \
./bin/wukongim-mqtt -config ./tmp/mqtt-quickstart/wukongim.toml
```

The source toolchain is Go 1.25.11. Keep the default 256 hash slots. Wait for
startup, then register Alice and Bob **from the trusted backend/protected test
environment**, using WuKongIM HTTP API on loopback:

```bash
curl -sS http://127.0.0.1:5001/user/token \
  -H 'Content-Type: application/json' \
  -d '{"uid":"alice","token":"alice-local-only","device_flag":1,"device_level":1}'
curl -sS http://127.0.0.1:5001/user/token \
  -H 'Content-Type: application/json' \
  -d '{"uid":"bob","token":"bob-local-only","device_flag":1,"device_level":1}'
```

From this example directory:

```bash
npm ci --no-audit --no-fund
MQTT_URL=mqtt://127.0.0.1:1883 \
MQTT_ALICE_TOKEN=alice-local-only \
MQTT_BOB_TOKEN=bob-local-only \
npm start
```

`MQTT_BOB_URL` optionally places Bob on another ingress in the same cluster.
URLs must use `mqtt://` (raw TCP) or `mqtts://` (an upstream TLS terminator),
without embedded credentials. There is no MQTT WebSocket listener. Never
disable TLS certificate verification for a public endpoint. Production login
and token issuance belong in your business backend, not this client.

The program creates ephemeral ClientIDs, authenticates with device flag `1`,
subscribes to each user's exact base64url inbox, waits for both SUBACKs, then
exchanges two QoS 1 messages. Each UTF-8 JSON payload uses the SDK text format
`{"type":1,"content":"..."}` and an independent `wk.client_msg_no`. It checks
recipient bytes, sender, channel type, application number and decimal-string
MessageID/sequence independently of successful PUBACK. The JSON success output
contains no tokens or payloads. PUBACK proves server commit, not reading or
business completion.

Subscription and publish acknowledgement waits are bounded at 10 seconds;
receive waits at 20 seconds; the complete program is bounded at
60 seconds, including normal DISCONNECT/cleanup. Failures exit nonzero and
print fixed diagnostics. The program intentionally makes one attempt. For an
uncertain business retry, retain the original application number/body.

This is an online exchange demonstration, not persistent-session, Will or
performance acceptance. Follow the bilingual site chapters for those contracts.
After the example exits, stop the development cluster with Ctrl+C.

`npm run check` only checks syntax. Live validation runs the exact file through
`test/e2e/mqtt/docs_quickstart` in single-node and three-node clusters with
256 hash slots, including rejected credentials. See that scenario's `AGENTS.md`.
