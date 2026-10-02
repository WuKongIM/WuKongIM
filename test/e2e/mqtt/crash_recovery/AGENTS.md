# MQTT owner crash recovery scenario

SIGKILL the MQTT owner process group while a QoS 1 delivery is unacknowledged,
then start the same node spec again. Use 256 hash Slots, Paho manual PUBACK with
Receive Maximum 1, native WKProto producers and HTTP-provisioned credentials.
Never resubscribe or inspect internal state. The resumed Session must report
Session Present, replay the same Packet Identifier with DUP and stable message
identity, then deliver a message sent after recovery exactly once.

Run: `WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-crash-reports GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/crash_recovery -count=1 -timeout=5m -v`

Reports record topology and public assertions without credentials or payloads.
