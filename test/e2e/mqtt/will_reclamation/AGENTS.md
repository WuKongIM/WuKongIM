# Will journal reclamation acceptance

Use real product processes, independent Paho MQTT 5 and public HTTP provisioning
in single-node and three-node clusters with 256 hash Slots. Temporary gofail may
reduce journal admission capacity, fail exact cleanup and pause Started work.
Use the same ClientID for successive lifetimes so all Wills share a Slot; verify
actual full-journal refusal on the captured executor. Never read storage files.
Restart only that owned process. Persistent reconnect must not SUBSCRIBE again.
Quiet observation must expire by deadline rather than a closed transport.
Write bounded JSON after all cleanup without credentials, payloads or identities.

Run: `WK_E2E_GOFAIL_MQTT=1 WK_E2E_BINARY=/tmp/wukongim-will-reclamation-gofail WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-will-reclamation GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/will_reclamation -count=1 -timeout=6m -v`.
