# MQTT qualification retirement E2E

Use real single-node/three-node clusters with 256 hash Slots, Eclipse Paho,
public WKProto and trusted HTTP provisioning. Observe fixed public maintenance
metrics before reconnecting ended lifetimes. Clean Start must establish a new
qualification before observing old cleanup; then a fresh person source must
still deliver through the new subscription. Never read tables or inject rows.

Run: `WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-qualification-reports GOWORK=off go test -p 2 -tags=e2e ./test/e2e/mqtt/qualification -count=1 -timeout=6m -v`

Record bounded JSON assertions without credentials or message bodies. This suite
proves qualification retirement, not metadata reclamation or owner-crash recovery.
