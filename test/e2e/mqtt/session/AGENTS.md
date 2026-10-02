# MQTT persistent Session scenario

Use real product processes with 256 hash Slots, independent Paho manual PUBACK
and Receive Maximum 1, native WKProto producers and HTTP-provisioned credentials.
Never resubscribe a resumed session or inspect internal state. Compare Packet
Identifier, DUP, body and stable message identity across reconnect/takeover.
The future-source message must be the new contact's first send while offline.

Run: `WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-session-reports GOWORK=off go test -p 2 -tags=e2e ./test/e2e/mqtt/session -count=1 -timeout=4m -v`

Reports record topology and public assertions without credentials or payloads.
Live-node takeover does not prove node-crash/partition recovery.
Graceful restart must preserve unacknowledged delivery without resubscription;
active-owner TERM must exit successfully without lifecycle stop failures.
