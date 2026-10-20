# Follow-up fixture failure, before correction

The first all-topology run on product 98c25091bb9587a303af982bb14bf4cedd9aa110 failed only 3-to-5. All three imported ordinary rows matched through each of five ingresses. The first WKProto append succeeded with ACK sequence 6; the fixture expected literal 4 and stopped before checking appended history or restarting.

Ranked falsifiable explanations:
1. The fixture assumes gap-free allocation, contrary to the existing runtime and migration contract. Existing recovery barriers consume log positions and the same scenario's HTTP appends already require strictly increasing sequences. Check public migration reference and exact full history after retaining the returned ACK position.
2. Unexpected duplicate business rows consumed positions. Complete equality of the returned ordinary-history list (count, IDs, ACK sequences, sender, client keys, payloads and header) must still fail if extra/missing business rows exist.
3. ACK/history disagreement or original-history mutation. Every ingress and later restart/failover must retain the exact acknowledged position and unchanged original rows; successful ACK alone is insufficient.

Evidence: docs-site/content/docs/server/operations/v2-to-v3-migration-reference.en.mdx:248 explicitly forbids assuming N+1 after startup/restart; recovery_barrier.go appends at recovered.LEO+1; existing channel_failover checks use monotonic business sequences. The failed log does NOT prove positions 4 and 5 were two specific barriers.

Change only the first-append fixture assertion to > previous ordinary sequence, and bind the expected row to the exact ACK sequence. Keep complete list equality at every ingress, unchanged original 1..3 migration mapping, authentication, idempotency, restart-before-append, failover and original deadlines. Preserve the same frozen product binary; freeze the revised harness separately. Run the complete five-topology package once and remaining planned controls; no until-green retries.
