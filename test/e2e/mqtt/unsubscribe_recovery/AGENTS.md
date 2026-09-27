# MQTT interrupted unsubscribe E2E

Use real product processes, standard Eclipse Paho MQTT 5 clients, WKProto
producers, trusted HTTP provisioning and public fixed metrics. Both deployment
shapes use 256 hash Slots. Never inspect tables or import server internals.

The opt-in suite requires a temporary-copy gofail build. Faults may interrupt
control flow at documented committed-intent/projection boundaries; they must not
write fixtures into storage or manufacture successful receipts. Verify each
selected failpoint executed, require background completion before reconnect, then
check Session Present, original PacketID/DUP/message identity and fresh delivery
after resubscribe. Emit bounded JSON without credentials or payloads. A controlled
response failure is not an abrupt process-crash or partition-isolation proof.

Build: `scripts/build-gofail-binary.sh --package internal/usecase/mqttsession --out /tmp/wukongim-mqtt-gofail`
Run: `WK_E2E_BINARY=/tmp/wukongim-mqtt-gofail WK_E2E_GOFAIL_MQTT=1 WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-unsubscribe-fault-reports GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/unsubscribe_recovery -count=1 -timeout=10m -v`
