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
