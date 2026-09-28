# MQTT group scale E2E

Single-node cluster, 256 hash Slots. Provision a 100,000-member group through
public HTTP, connect K persistent Paho subscribers, send through WKProto and
churn UNSUBSCRIBE/SUBSCRIBE on live Sessions. Assert exactly-once, ordered,
identity-preserving delivery and that the fixed `retired` consumer counter
covers every churn tombstone. Never read tables or inject rows. See
`docs/specs/mqtt-scale-acceptance.md`.

Run: `WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-scale-reports GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/scale -count=1 -timeout=15m -v`

Sizes: `WK_E2E_MQTT_SCALE_MEMBERS`, `_CONNECTIONS`, `_MESSAGES`, `_CHURN`,
`_ROUNDS`. The artifact records configured and observed sizes; it is not a
capacity claim beyond them.
