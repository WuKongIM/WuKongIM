# MQTT interrupted subscribe E2E

Use real product processes, Eclipse Paho MQTT 5, WKProto producers, trusted
HTTP provisioning and fixed public metrics. Keep 256 hash Slots in both
single-node and three-node clusters; never inspect storage or server internals.

The opt-in suite uses temporary-copy gofail builds. Interrupt after committed
Preparing intent, and separately before the background final Active commit.
Verify failpoint hits and background completion before reconnect. Resume without
another SUBSCRIBE and verify original message identity, the first source boundary
and future delivery. Emit bounded JSON without credentials or payloads. These
controlled request failures do not prove abrupt crash or partition isolation.

`TestReplayConfirmationRejectsMixedReplicaFailures` combines a hard evidence or
callback failure on replica 1 with a temporary yield on replica 2, using inert
comment-only failpoints in the joined confirmation cohort. Require at least
three hits, the fixed hard-error closure counter and no establishment completion
before disabling faults. Resume without SUBSCRIBE and verify the same original
boundary plus offline/future identity. Never manufacture a successful receipt.

Build: `scripts/build-gofail-binary.sh --package internal/usecase/mqttsession --out /tmp/wukongim-mqtt-gofail`
Run: `WK_E2E_BINARY=/tmp/wukongim-mqtt-gofail WK_E2E_GOFAIL_MQTT=1 WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-subscribe-fault-reports GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/subscribe_recovery -count=1 -timeout=10m -v`
