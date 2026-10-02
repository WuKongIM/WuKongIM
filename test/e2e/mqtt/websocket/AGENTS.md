# MQTT WebSocket scenario

Use real cmd/wukongim processes, independent Eclipse Paho packet encoding,
WebSocket clients and trusted Product HTTP credential provisioning. Keep 256 hash
slots in single-node and three-node clusters. Never import product internals or
read MQTT storage. Common protocol helpers belong in test/e2e/suite.

Failure inventory (written before implementation): missing/wrong subprotocol
acceptance; wrong path acceptance; text data accepted as MQTT; nonbinary replies;
CONNECT or PUBLISH lost across WebSocket messages/continuations; coalesced packets
lost; failed authentication accepted; changed message bytes/identity; cross-node
routing failure; oversized messages admitted; leaked client/process resources;
credential or message-body disclosure in evidence.

Run:
`WK_E2E_MQTT_REPORT_DIR=/absolute/artifact-directory GOWORK=off go test -tags=e2e ./test/e2e/mqtt/websocket -count=1 -timeout=5m -p=1 -v`.

Reports contain topology, hash-slot count, binary identity and bounded protocol
assertions, never credentials or message bodies. Native Chromium MQTT.js execution
is covered independently by the MQTT Demo acceptance. This scenario does not
claim complete fault/load qualification or native TLS termination.
