# Three-Node Backup Restore Scenario

This scenario proves the multi-node replacement backup workflow through public
Manager and business APIs:

1. all three nodes use one shared file repository;
2. an online full backup survives Controller Leader loss and resumes;
3. the stopped node rejoins before restore;
4. restore stages every current replica and restores exact point-in-time
   business state.

Do not inspect node data directories or repository objects to decide success.
Keep failure polling bounded and include cluster diagnostics.

Checkpoint user, group and canonical person-channel bans before publication;
mutate them and create a later-only user ban before restore. All three ingress
nodes must observe the restored values/versions and reject prohibited sends,
with one-hour auxiliary caches enabled. The artifact base is configurable with
`WK_E2E_SEND_BAN_RESTORE_REPORT`; this scenario appends `-3-node.json`.
After Controller success, require public readiness on every node before policy
assertions: a peer may still be applying maintenance release. Do not poll stale
policy values until they happen to match.
