# controller_leader_failover AGENTS

This scenario proves the Controller Leader coordinator resumes one persisted
incremental backup job after a process failover and node rejoin.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/backup/controller_leader_failover -count=1 -timeout 3m -p=1
```

## Rules

- Use only public Manager and metrics entrypoints for assertions.
- Rejoin the stopped combined Controller/data node after a different
  Controller Leader has demonstrably taken over the same job.
- Do not treat this scenario as sustained data-node outage, S3, KMS, or Object
  Lock production qualification.
