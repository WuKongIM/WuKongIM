# MQTT restore reactivation scenario

Use authenticated public Manager backup/restore HTTP and independent Paho MQTT 5
clients against real 256-hash-Slot single-node and three-node clusters with a
shared backup repository and an existing native group history. Keep cold first
subscription admission in its dedicated scenario. Back up an unacknowledged
QoS 1 exchange, restore twice without process restart, and never resubscribe the
restored Session or inspect internal tables. Prove maintenance refusal, previous
connection closure, Session Present, identical Packet Identifier/body/identity and
DUP replay, removal of post-backup state and exactly one fresh delivery.
Race 16 bounded CONNECTs with each restore, and resume through another ingress
in the three-node cluster to exercise remote exact-owner retirement RPC.
After Controller completion, await every node’s public HTTP readiness before
one MQTT reconnect attempt; never retry MQTT to conceal admission failure.

Reusable public backup client support belongs in `test/e2e/suite`. Reports must
contain bounded public assertions/counts without credentials or payloads.

Run: `WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-restore-reports GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/restore -count=1 -timeout=25m -v`

The opt-in unavailable-cleanup case uses a temporary backup gofail build. Enable
the inert archive-lease release failure after backup, preserve an erroneous
restore response, and observe public ActiveRestore without retrying a mutation.
Successful admission must consume its exact lease atomically: the old cleanup
fault stays uncalled while both restore/MQTT cycles complete. Controlled failure
proves this response contract, not the cause of earlier unobserved HTTP 503s.
Build with `scripts/build-gofail-binary.sh --package internal/usecase/backup`;
run with `WK_E2E_GOFAIL_MQTT=1` and the explicit binary/report paths.
