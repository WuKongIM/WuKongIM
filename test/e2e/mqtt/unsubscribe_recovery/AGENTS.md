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

`TestColdGroupSubscriptionAdmission` is focused first-subscription coverage
and a diagnostic loop derived from this scenario. A passing run does not prove
the intermittent setup failure is repaired. It needs no gofail build: one real three-node
256-Slot cluster attempts 64 fresh persistent Sessions/groups without retrying
SUBSCRIBE, publications or unsubscribe faults. It emits a bounded JSON result on
success or failure, including fixed closure observations and per-attempt latency.
Run it with `GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/unsubscribe_recovery -run '^TestColdGroupSubscriptionAdmission$' -count=1 -timeout=3m -v`.

`TestColdStartupGroupSubscriptionAdmission` uses the same first-subscription
assertions but performs only one admission per fresh three-node cluster. Use
`-count=N -failfast` with an explicit bounded timeout to distinguish process
startup from repeated cold groups in a warmed cluster. It emits
`mqtt-startup-group-subscribe.json`; repetitions overwrite that last-result file
while the test log retains every verdict. It also requires no enabled failpoint.

When `WK_E2E_GOFAIL_MQTT=1`, the startup-only loop also preserves the original
scenario's gofail HTTP endpoints and WaitListed readiness queries, but enables
no fault. This profile requires an instrumented binary; the default startup-only
profile works with an ordinary product binary. The artifact records the profile.
