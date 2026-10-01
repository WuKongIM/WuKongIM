# MQTT three-node Owner outage scenario

Use real product process groups with 256 hash Slots, three Slot replicas and two
Channel replicas. SIGSTOP keeps the old process alive; SIGKILL has no graceful
retirement. Target CONNECTs on both surviving ingresses must fail before and
after the 30-second grant, with surviving quorum and unrelated CONNECT controls.
Resume or restart the exact old node, wait for public convergence and isolate
the old Owner through the existing product contract before successful takeover.

Independent Paho manual PUBACK/Receive Maximum 1 must preserve Session Present,
Packet Identifier, DUP, body and original native identity through two cross-node
handoffs without SUBSCRIBE. Require old-client closure and one fresh delivery.
Never inspect MQTT tables or retry an ambiguous target CONNECT as success.
Resume any suspended process before harness cleanup; join killed groups before
restart. Reports run after cleanup and contain only bounded assertions/counts.
This is process unavailability, not full network-partition acceptance.

Run: `WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-owner-outage GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/owner_outage -count=1 -timeout=8m -v`
