# Scheduled Restore Scenario

This scenario owns the smallest process-level proof of the replacement backup
workflow:

1. enable a daily file-repository plan through authenticated Manager HTTP;
2. wait for the immediate initial full archive;
3. create post-backup business data;
4. restore with explicit permission, password reauthentication, and the exact
   confirmation phrase;
5. prove the post-backup data disappeared while the backed-up data remains.

Keep polling bounded and include process diagnostics on failure.

The checkpoint includes user, group and canonical person-channel send bans.
Change them after publication, create a later-only user ban, then assert restore
reinstates exact values/versions and removes the later row. Warm auxiliary
permission caches with a one-hour TTL and reject new sends after restore.
`WK_E2E_SEND_BAN_RESTORE_REPORT` selects the JSON artifact base; this scenario
appends `-1-node.json`.
