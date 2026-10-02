# Will proposal and apply recovery

Use real product processes and Paho MQTT 5 with public provisioning and 256 hash
Slots. Never read storage or import product internals. Queued-before-RawNode and
committed-before-FSM cuts apply to single-node and three-node clusters. Persisted
uncommitted replication applies only to a three-node cluster, with the reached
post-persistence/index-above-commit marker required before claiming that state.

Keep command faults in temporary gofail builds. Do not fabricate CAS results,
change command bytes or count a delayed applied reply as delayed commit/apply.
Persistent reconnect must retain the original subscription. Quiet must return
DeadlineExceeded; join owned clients and processes before body-free JSON.

Run with `WK_E2E_GOFAIL_MQTT=1 WK_E2E_BINARY=/tmp/wukongim-will-raft-gofail
WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-will-raft GOWORK=off go test -p 1 -tags=e2e
./test/e2e/mqtt/will_raft_recovery -count=1 -timeout=25m -v`.
