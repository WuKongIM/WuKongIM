# data_node_outage AGENTS

This scenario proves one persisted incremental backup job can finish while one
combined Controller/data node remains offline. It uses three Slot replicas and
two Channel replicas so one unavailable node preserves both Slot quorum and the
declared Channel placement durability.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/backup/data_node_outage -count=1 -timeout 8m -p=1
```

## Rules

- Stop a non-Controller-Leader data node only after the incremental job is
  observably active; the selected node must lead at least one Slot before the
  fault.
- Keep the stopped node offline through exact restore-point verification.
- Require both surviving nodes to recover public readiness after the fault and
  remain ready after exact restore-point verification.
- Use only public Manager, HTTP readiness, and metrics entrypoints for
  assertions.
- Do not use this scenario to claim readiness for a three-node cluster whose
  Channel replica count is three: that topology correctly refuses new Channel
  placement while one data node is unavailable.
- Do not treat this scenario as S3, KMS, or Object Lock production
  qualification.
