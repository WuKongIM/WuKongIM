# stream_online Scenario

Prove public stream EVENT delivery over JSON-RPC WebSocket in real 256-hash-slot
single-node and three-node clusters. Use public HTTP, SDK-compatible JSON-RPC,
and offline history only; do not import internal packages or storage internals.

Cover group and person recipient routing, delta/finish/error/cancel, private
visibility, invalid base rejection, and terminal offline snapshot recovery.
The browser acceptance in demo/streamdemo executes the pinned EasySDK package.

Run:
`WK_E2E_STREAM_REPORT=/tmp/wk-stream-online.json GOWORK=off go test -tags=e2e ./test/e2e/message/stream_online -count=1 -timeout=3m -p=1 -v`

The JSON report is repeatable evidence of the successful public-protocol cases.
