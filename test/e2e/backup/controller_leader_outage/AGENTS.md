# controller_leader_outage AGENTS

This scenario proves one persisted incremental backup job can finish after its
Controller Leader and combined data-node role remain offline. It uses three
Slot replicas and two Channel replicas so the surviving nodes preserve Slot
quorum and the declared Channel placement durability.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/backup/controller_leader_outage -count=1 -timeout 3m -p=1
```

## Rules

- Stop the current Controller Leader only after the incremental job is
  observably active; immediately before the fault it must also lead at least
  one Slot.
- Keep the stopped combined Controller/data node offline through exact
  restore-point verification.
- Require both surviving nodes to recover public readiness after the fault and
  remain ready after exact restore-point verification.
- Require the new Controller Leader to expose the same persisted job and
  restore-point identity through public Manager and metrics entrypoints.
- Do not use this scenario to claim readiness for a three-node cluster whose
  Channel replica count is three.
- Do not treat this scenario as S3, KMS, or Object Lock production
  qualification.
