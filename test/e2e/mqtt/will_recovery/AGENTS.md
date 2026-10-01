# MQTT Started Will recovery scenario

Use independent Paho MQTT 5 against real product processes, 256 hash Slots and
both single-node and three-node clusters. Provision through public HTTP only.
Temporary gofail controls cut after Started and after real publication, and delay
an accepted Will append beyond the execution grant. Restart only the exact
harness-owned executor group, never inspect MQTT tables. A persistent recipient
reconnects once without SUBSCRIBE; retain original PacketID/DUP/identity where
the exchange began before the crash. Quiet observation must end by deadline,
not closed transport. Reports run after process/client cleanup and exclude
credentials, payloads, client identities and raw logs.

Run: `WK_E2E_GOFAIL_MQTT=1 WK_E2E_BINARY=/tmp/wukongim-will-gofail WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-will-recovery GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/will_recovery -count=1 -timeout=12m -v`.

See `docs/specs/mqtt-will-started-recovery.md` for the failure inventory and
strict non-dispatch versus unknown-effect proof limits.
