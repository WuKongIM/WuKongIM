# Plugin send ban E2E

Run `GOWORK=off go test -tags=e2e ./test/e2e/plugin/send_ban -count=1 -timeout=4m -v`.

- Use real single-node and three-node clusters with 256 Hash Slots and a real
  `.wkp` process using the public legacy host RPC protocol.
- Write policies through public HTTP on node 1; plugin sends enter node 1 or 3.
- Verify successful sends before/after bans, explicit host RPC failure for
  rejected sends, exact committed history and absence of rejected delivery.
- Gate one already-admitted SEND inside the real plugin hook, complete the ban,
  then release it. That in-flight SEND may succeed; all subsequent SENDs fail.
  Read history while the sender is still banned to prove reads remain allowed.
- The fixture accepts sequential, atomic sandbox command files and writes
  responses atomically. Every command has a stable client message number.
- Do not retry plugin RPC failures or accept MessageId zero as a successful SEND.
- Keep the permission cache TTL at one hour. No sleeps replace policy freshness.
- Emit JSON with source identity, topology, request/result timestamps and verdict;
  `WK_E2E_PLUGIN_SEND_BAN_REPORT` overrides the output path.
