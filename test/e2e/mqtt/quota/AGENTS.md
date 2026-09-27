# MQTT consumer maintenance E2E

Use real single-node and three-node product clusters with 256 hash Slots,
trusted HTTP provisioning, WKProto producers and independent Paho clients.
Offline quota cases keep the receiver disconnected until public metrics confirm
ending, Channel-source removal and UID qualification retirement. Full-window cases retain one unacknowledged exchange.
Reconnect once without Clean Start; require Session Present 0, then subscribe and
receive a new message. Completion cases ACK, observe projected progress,
unsubscribe and observe source removal. Never inspect tables or substitute an
online quota path for offline assertions. Sum public fixed metrics across nodes.

Run: `WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-consumer-reports GOWORK=off go test -p 2 -tags=e2e ./test/e2e/mqtt/quota -count=1 -timeout=8m -v`

Emit bounded JSON assertions without payloads or credentials. These cases prove
Session quotas and Channel-source progress, not full cleanup or scale acceptance.
