# MQTT Will scenario

Use real product processes and independent Paho clients with durable HTTP-provisioned
credentials. Cover single-node and three-node clusters with 256 hash Slots.
Observe Will Delay, abnormal TCP closure, normal DISCONNECT cancellation and
publication identity through MQTT only; do not inspect storage or call usecases.

Run: `GOWORK=off go test -p 2 -tags=e2e ./test/e2e/mqtt/will -count=1 -timeout=4m -v`

Set `WK_E2E_MQTT_REPORT_DIR` to preserve topology/assertion JSON results. This
scenario does not establish crash-window redispatch or unavailable-owner recovery.
