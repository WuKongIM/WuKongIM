# MQTT documentation quickstart

Run the exact Node.js MQTT.js example from `docs-site/examples/mqtt-quickstart`
against real authenticated single-node and three-node clusters with 256 hash
slots. The user explicitly selected MQTT.js for this tutorial; existing Paho
interop coverage remains independent. Use trusted Product HTTP only to provision
test credentials. Do not import product internals or read MQTT storage.

Before implementation, distinguish failed authentication, premature publish,
payload/identity corruption, missing reply, cross-node delivery, leaked clients,
and credential disclosure. Node execution has a bounded deadline. Reports retain
topology, pinned client version, message identities, source hashes and pass/fail;
never retain tokens or message bodies.

Prerequisites: Node.js >=20.11; `npm ci` in the example directory.
Run: `GOWORK=off go test -tags=e2e ./test/e2e/mqtt/docs_quickstart -count=1 -timeout=4m -p=1 -v`.
Set `WK_E2E_MQTT_REPORT_DIR` to preserve the two JSON receipts.
