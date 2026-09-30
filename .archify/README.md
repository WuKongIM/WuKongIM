# WuKongIM architecture diagrams

These Archify diagrams describe repository revision
`77c8c71dd394402035641d1c42faf164f3155f27`. Open the HTML files in a browser to
explore nodes, source references, interaction details, themes, and exports.

| Diagram | Interactive artifact | Specification |
| --- | --- | --- |
| System architecture | [HTML](architecture-wukongim-system-20260930-151958/wukongim-system.html) | [JSON](architecture-wukongim-system-20260930-151958/candidate.json) |
| Message sending workflow | [HTML](workflow-wukongim-send-message-20260930-153930/wukongim-send-message.html) | [JSON](workflow-wukongim-send-message-20260930-153930/candidate.json) |
| Multi-node sending and delivery | [HTML](sequence-wukongim-cluster-send-20260930-160349/wukongim-cluster-send.html) | [JSON](sequence-wukongim-cluster-send-20260930-160349/candidate.json) |

Each directory's `handoff.json` identifies its final artifact hashes and passing
validation and browser receipts. Earlier receipts and alternate candidates are
retained as the generation history; use the handoff's referenced receipts for
the final result. Capture directories contain light and dark screenshots at
1440×900 and 2048×1320, inspected during delivery.

The multi-node sequence uses illustrative nodes A–F and a three-voter Channel
on C/D/E. Roles can share a physical node or move between nodes. Slot authorities
may be distributed across the cluster. SENDACK completion and online delivery
are independent paths; receiver RECVACK state belongs to the connection Owner.
