# Original ordinary-history migration

Use only the unchanged original v2 server fixtures and their recorded HTTP
requests/acknowledgements. Do not rewrite source storage or import conversion
internals. Cover successful migration across source/target node counts, original
credentials and history, sequential appends, idempotency, full restart and
Channel Leader failover through the real CLI, HTTP and WKProto interfaces.

History recovery keeps one 20-second deadline per read and 100 ms retry spacing.
Retry only HTTP 503 with valid JSON containing the exact top-level keys
`code: "unavailable"` and numeric `status: 503`. All other HTTP statuses,
unstructured or different 503 responses, malformed envelopes, and transport
errors fail immediately. Never match internal error substrings or retry an
incorrect successful history page.

Keep imported ordinary positions exactly `1..N`. Native recovery barriers may
consume positions before any later append, including the first WKProto send.
Require each new business sequence to exceed the preceding business sequence
and bind every expected history row to the exact returned ACK sequence; never
assume `N+1`. Extra, missing, duplicated or mismatched ordinary rows still fail.

Keep complete expected history comparisons at every ingress, retained-credential
authentication, append/idempotency assertions, whole-cluster restart before
another append, and applicable Channel Leader failover. Each successful history
check logs its phase, node, row count and temporary-retry count; failures retain
the phase/node and existing bounded public/process diagnostics.

After the first imported-history read, wait for the target ingress's real
WKProto readiness before authenticating with the retained v2 credential.
HTTP `/readyz` alone does not prove that protocol listener has started; keep
the original credential assertion and do not retry business Connect or SEND.

Run the original regression slice with
`GOWORK=off go test -tags=e2e ./test/e2e/migration/ordinary_history -run '^TestOriginalOrdinaryHistoryTopology$/^1_source_nodes_to_3_target_nodes$' -count=1 -timeout=12m -p=1 -parallel=1 -json`,
then run the same command without `-run` for all five topology cases. Preserve
each JSONL output together with the source revision and binary digest. Set
`WK_E2E_BINARY` to the exact prebuilt candidate to avoid rebuilding the server.
