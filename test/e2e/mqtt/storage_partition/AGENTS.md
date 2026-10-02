# MQTT storage partition E2E

Use three live product processes, 256 hash Slots and three Channel replicas.
Partition cluster TCP through suite-owned relays; retain public listeners and
timers. Require actual relay activity in both directions and public surviving
Slot quorum. Never substitute SIGSTOP, HTTP refusal or an unhit failpoint for
network isolation. Never inspect storage rows or import product internals.

Persistent Paho consumers use manual ACK/Receive Maximum 1. Preserve original
identity and unfinished PacketID/DUP without SUBSCRIBE after healing. Independent
ACKs must retain shared-body capacity until the final proved retirement.
Unknown physical preparation uses only an existing temporary-copy gofail cut;
require its hit and retained debt before recovery. Keep fixed bounded deadlines.
Publish a bounded JSON receipt after all client, relay and process cleanup.
