# Sealed Started rejection scenario

Use real product processes, independent Paho MQTT 5, public product policy and
temporary gofail controls. Keep 256 hash Slots and one logical Slot group with a
cap-one journal to prove pressure on the captured executor. Never read MQTT rows
or attempt files. Do not retry CONNECT or SUBSCRIBE on recovery. Join each client
and exact killed process; emit bounded JSON after cleanup. Silence must end with
the deadline on a healthy receiver. See `docs/specs/mqtt-will-sealed-rejection.md`.

Run: `WK_E2E_GOFAIL_MQTT=1 WK_E2E_BINARY=/tmp/wukongim-will-sealed-gofail WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-will-sealed GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/will_sealed_rejection -count=1 -timeout=15m -v`.
