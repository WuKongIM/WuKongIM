# wkcloudbundle Flow

`wkcloudbundle` keeps the legacy run-specific Cloud Simulation bundle commands
and the new procurement-independent offline deployment bundle commands separate.

```text
legacy render / verify
  -> internal/infra/cloudsim/deploy compatibility bundle

seal-offline --root --source-sha --control-sha
  -> internal/usecase/clouddeploy fixed Ubuntu four-host intent and validation
  -> internal/infra/clouddeploy no-follow filesystem adapter
  -> static validation without services or cloud access
  -> bundle-manifest.json with content SHA-256

verify-offline --root
  -> recompute intent, file inventory, modes, sizes, and SHA-256
```

No command clones source, builds software, reads credentials, starts background
work, or performs cloud discovery. The trusted build Workflow owns compilation
and checksum-pinned dependency download before calling `seal-offline`.
