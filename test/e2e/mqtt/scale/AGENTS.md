# MQTT group scale E2E

Single-node cluster, 256 hash Slots. Provision a 100,000-member group through
public HTTP, connect K persistent Paho subscribers, send through WKProto and
churn UNSUBSCRIBE/SUBSCRIBE on live Sessions. Assert exactly-once, ordered,
identity-preserving delivery and that the fixed `retired` consumer counter
covers every churn tombstone. Never read tables or inject rows. See
`docs/specs/mqtt-scale-acceptance.md`.

Run: `WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-scale-reports GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/scale -count=1 -timeout=15m -v`

Sizes: `WK_E2E_MQTT_SCALE_MEMBERS`, `_CONNECTIONS`, `_MESSAGES`, `_CHURN`,
`_ROUNDS`. The artifact records configured and observed sizes; it is not a
capacity claim beyond them.

The bounded preparation regression is opt-in and isolates public HTTP setup:
`WK_E2E_MQTT_MEMBERSHIP_PROBE=1 WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-membership GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/scale -run TestGroupMembershipProvisioningBudget -count=1 -timeout=2m -v`.
It prepares exactly 10,000 members within 30 seconds, verifies public membership
row metrics and sampled conversation hydration, and emits `mqtt-membership.json`
on success or failure. It does not qualify MQTT delivery or production capacity.
`WK_E2E_MQTT_MEMBERSHIP_PROFILE=1` enables loopback debug profiling for a separate
diagnostic attempt; keep profiling disabled in acceptance measurements.

Scale logs each completed phase and emits `mqtt-scale-failure.json` on failure
with confirmed member count and configured workload, without credentials.
After all initial subscriptions succeed, failures also emit
`mqtt-scale-delivery-failure.json` with bounded receipt histograms and independent
Paho closed/incomplete counts, followed by a bounded harness process dump before
cleanup. A closed incomplete persistent client fails the workload early because
it cannot receive future publications; open clients retain the original receipt
deadline. Never reduce the receipt count or reconnect a failed client.
`WK_E2E_MQTT_SCALE_PROFILE=1` enables loopback debug profiling only for separate
diagnostic runs; full acceptance must leave it unset.

The authenticated WKProto sender issues real PING/PONG every 15 seconds while
waiting through fanout/churn/retirement, because Gateway closes connections after
three minutes without inbound activity. Join the one bounded heartbeat loop on
cleanup, fail on heartbeat errors and record its count. Never reconnect or change
message identity to mask a broken sender.

The opt-in in-flight churn regression sends twenty acknowledged messages to a
2,000-member group and churns before all initial receipts arrive. It defaults to
500 persistent connections, three rounds and at most 200 churners. Verify every
control reply, exact ordered/unique observed message identities, one fresh
post-churn delivery per client and all churn retirements. Unadmitted old messages
need not survive removal. It emits `mqtt-churn-in-flight.json` on success/failure
with phase, reply/receipt counts, retirement and fixed aggregate closure labels.
Run: `WK_E2E_MQTT_CHURN_PROBE=1 WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-churn GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/scale -run TestGroupChurnWithInFlightDelivery -count=1 -timeout=6m -v`.
`WK_E2E_MQTT_CHURN_PROBE_CONNECTIONS` narrows the contention loop;
`WK_E2E_MQTT_CHURN_PROBE_PROFILE=1` is diagnostic only. This is separate from the
unchanged 100,000-member full scale acceptance.
