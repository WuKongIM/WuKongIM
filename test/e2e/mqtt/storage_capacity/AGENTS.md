# MQTT aggregate storage capacity E2E

Use real product processes, Paho MQTT 5 Sessions, authenticated WKProto
producers, and public metrics only. Keep 256 hash Slots in both topologies.
Use separate protected sources and individually compliant offline Sessions
to distinguish shared storage capacity from per-Session quotas. Preserve
every accepted message across joined restart and release capacity only through
normal delivery completion and proved replay retirement.

Emit bounded JSON receipts on success and failure. Never inspect MQTT rows.
