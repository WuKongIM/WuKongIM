# MQTT interop scenario

Prove authenticated MQTT 5 and WKProto person messages cross protocols without
changing payload or stable server identity, in single-node and three-node
clusters. Verify advertised capabilities and that distinct MQTT ClientIDs using
one UID/device credential coexist with the user's WK connection.

Run:
`GOWORK=off go test -tags=e2e ./test/e2e/mqtt/interop -count=1 -timeout=4m -p=1 -v`

Set `WK_E2E_MQTT_REPORT_DIR` to preserve a JSON result for each topology. Reports
contain message IDs, topology and assertions, never tokens or payloads.
This scenario does not establish durable replay or Will correctness.
