# three_node_restore AGENTS

This scenario proves a real three-node source cluster can publish a baseline
and incremental restore point, record a later permanent erasure, then restore
the older point into a fresh three-node successor without resurrecting erased
messages or reusing their sequence numbers.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/backup/three_node_restore -count=1 -timeout 8m -p=1
```

## Rules

- Use only public Manager and message/client entrypoints for assertions.
- The local file repositories and deterministic local key authority are
  harness dependencies available only in the e2e-tagged product binary.
- Do not treat this local drill as S3/KMS/Object-Lock production qualification.
- Prove the source is stopped before activation, then restart the successor in
  normal mode before checking restored history and new writes.
