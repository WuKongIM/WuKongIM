# First MQTT group subscription admission

Use real 256-hash-Slot single-node and three-node clusters, public Product HTTP
provisioning/send, independent Paho MQTT 5 and public fixed metrics. Every round
uses a new group and ClientID with one initial SUBSCRIBE. Cover empty history and
existing native history and rotate ingress nodes. Never retry CONNECT/SUBSCRIBE
or enlarge the product/client packet deadlines. Verify original future-message
identity and QoS 1 delivery after SUBACK. Preserve failures in bounded JSON
without credentials, group/client identities or message bodies.

Run: `WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-subscribe-admission GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/subscribe_admission -count=1 -timeout=25m -v`
