# Channel metadata create accounting E2E

Use three fresh `cmd/wukongim` processes, 256 hash slots, 12 logical Slots,
three Slot and Channel replicas, and the default authentication and timeouts.
Send ordinary person messages through two different ingress nodes, each remote
from the Channel's Slot leader. The scenario covers isolated and concurrent
first sends, a later send, and an exact Manager lookup after all nodes restart.
Compare only the authoritative v2 creation series across all three nodes.
Neither a successful SENDACK nor an already-existing counter substitutes for a
new durable insertion. The bounded receipt contains only Slot numbers, case
names, counters, and results. Write it after joining the node processes.

`WK_E2E_META_CREATE_REPORT=/absolute/new-report.json GOWORK=off go test -p 1 -tags=e2e ./test/e2e/message/meta_create_accounting -count=1 -timeout=3m -v`
