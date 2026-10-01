# Will CAS and reclamation races

Use real product processes and Paho MQTT 5 through public provisioning. Keep 256
hash Slots in single-node and three-node clusters. Temporary gofail may delay an
actual committed Started/successor/terminal CAS reply, hold the original Started
worker, and interrupt a reclamation page after fresh retirement evidence.

Cap two isolates the race; it is not production-cap stress. Reuse one ClientID
across lifetimes, identify the actual cut executor, and require full refusal on
that node. Startup failpoints must preserve counters/cap across exact restart.
Do not read storage or import product internals. Persistent reconnect keeps the
original subscription. Healthy quiet must end with DeadlineExceeded. Join every
owned client/process before writing bounded body-free JSON.

Run with a gofail binary including pkg/slot/proxy and the Will packages:
`WK_E2E_GOFAIL_MQTT=1 WK_E2E_BINARY=/tmp/wukongim-will-pressure-gofail WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-will-races GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/will_reclamation_races -count=1 -timeout=25m -v`.
