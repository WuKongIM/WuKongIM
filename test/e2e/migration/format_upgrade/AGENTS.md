# Format 1 startup upgrade

Use the unmodified v3.0.0-beta.21 binary (`WK_E2E_FORMAT1_BINARY`) to create a
three-node, 256-hash-Slot cluster. Use public HTTP/WKProto only for business data.
`WK_E2E_BINARY` selects the candidate. Never rewrite storage fixtures.

Run with `GOWORK=off go test -tags=e2e ./test/e2e/migration/format_upgrade -count=1 -timeout=15m -p=1 -v`.
Set `WK_E2E_FORMAT_UPGRADE_FAULTS=1` with a temporary-copy gofail candidate to
also kill during backup, after a durable metadata batch, and before publication.
The JSON receipt (`WK_E2E_FORMAT_UPGRADE_REPORT`, default under /tmp) binds both
binaries, every phase, backup hashes and public history observations. Backups
must also start successfully with beta.21 after copying to isolated data paths.
