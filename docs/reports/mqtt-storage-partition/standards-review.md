# Standards review

Read-only review against base `eb4c70b270193b22a520b8fe06b44ec72b19f333`, applying
root/E2E/MQTT/scenario AGENTS, applicable FLOW and the code-review smell baseline.
The reviewer made no writes and ran no tests.

Final source verdict: **no remaining actionable Standards findings**.

Closed documented gaps: both scenarios require all four directed partition
edges, the node-local exemption and surviving Slot quorum with actual leader
agreement. Important relay ownership fields have English constraint comments.
Closed judgment concerns: child output retains at most 4 KiB before allocation
can grow, and sender classification matches the complete accepted socket tuple
and an exact registered product PID.

Bridge admission and worker joining remain bounded. Cleanup registers clients
and product processes before relays and the final JSON receipt. Imports preserve
the black-box boundary. Runtime validation is separately recorded in the manifest.
