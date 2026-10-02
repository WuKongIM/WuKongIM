# MQTT Session reclamation E2E

Use real single-node and three-node clusters with 256 hash Slots, Eclipse Paho,
public WKProto and trusted HTTP provisioning. Never read tables or inject rows.
After zero-expiry disconnect, offline expiry or Clean Start, require the fixed
public reclamation completion and UID retirement counters before using the next
lifetime. A new person source must still deliver through that lifetime.

Record bounded JSON without credentials or message bodies. Completion metrics
are observations, not unique-session counts or proof of physical compaction.
This scenario does not prove unavailable-owner isolation, abrupt-crash recovery,
Will redispatch or large-scale performance.

Run: `WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-reclamation-reports GOWORK=off go test -p 2 -tags=e2e ./test/e2e/mqtt/reclamation -count=1 -timeout=6m -v`
