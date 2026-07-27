# three_node_restore AGENTS

This scenario proves a real three-node source cluster can continuously capture
and publish an immutable checkpoint, record a later permanent erasure, then
restore that checkpoint into a fresh three-node successor without resurrecting
erased messages or reusing their sequence numbers. The local drill also stops
the Controller Leader, an affected Slot Leader, a separate data node, and the
restore Controller Leader; corrupts one opaque repository copy and then both
copies; and proves repair or live-source Slot rebase through public evidence.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/backup/three_node_restore -count=1 -timeout 18m -p=1
```

`TestProductionStorageQualification` is an opt-in form of the same recovery
drill. Run it through `.github/workflows/backup-qualification.yml` so the
protected `backup-production` environment supplies real cross-region Alibaba
OSS, ordinary/repair/garbage RAM roles, one protected deployment key package,
and ObjectWorm policy without exposing values in logs. The production drill keeps the repositories real
while its e2e-only read wrapper injects corruption, so both repair roles are
exercised against OSS rather than merely assumed.

## Rules

- Use only public Manager and message/client entrypoints for assertions.
- The local file repositories and deterministic local key authority are
  harness dependencies available only in the e2e-tagged product binary.
- Do not treat this local drill as OSS/deployment-key/ObjectWorm production
  qualification.
- Production qualification must not set `WUKONGIM_BACKUP_E2E_FILE_ROOT`.
- Prove the source is stopped before activation, then restart the successor in
  normal mode before checking restored history and new writes.
