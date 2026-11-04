# Automatic v3 directory format 1 to 2 upgrade

The server upgrades registered `wukongim-v3` format-1 node directories before
opening product runtimes or listeners. This includes beta.21 data. Stop every
node, install matching new server binaries everywhere, preserve the original
configurations, then start the nodes. Rolling upgrades and mixed old/new nodes
are unsupported.

## Startup and interruption

Each node takes its migration ownership lock and the existing Pebble locks,
refusing active old writers. It copies the stopped generation into
`DATA_DIR/format-upgrade-v1-v2/backup/data/`. An external Controller directory
is copied into `backup/controller/`; `state.json` records its original path.
The archive-only `backup-repository` mount is excluded. Special durable files
are refused. Copies use independent files, private permissions and synchronous
publication; SHA-256 for every regular file is sealed in
`backup-manifest.jsonl` before any database is opened for writing.

The initial disk preflight requires free space of at least twice the source
bytes; retries credit already copied, checksum-matching files. Metadata conversion scans at most 128 keys or approximately 1 MiB per
synchronous batch, committing its continuation key with the changed records.
Legacy `Channel(UID, person).send_ban` becomes `User.send_ban`, with revision 1
for active bans, preserving credentials and creating policy-only users when
needed. Its person-Channel flag is cleared; other Channel flags are retained
with initial policy revision 0. Message IDs/sequences, subscriber identities,
Controller and Raft history remain intact. Old snapshots and pre-policy-version
TLV Channel command replay receive the same policy translation.

Only after conversion and store close does an atomic marker replacement publish
format 2. Original `created_by` and `created_at` are preserved. Normal cluster
recovery/admission follows. stderr reports migration before logger creation,
with repeated batch progress throttled to five seconds. No new configuration
is required.

After an interrupted start, run the same binary with the same paths. Completed
identical backup files are reused. A sealed backup and manifest are verified
before resuming conversion. A failed check leaves the marker at 1 and prevents
runtime admission. Never edit the marker, delete upgrade progress, modify the
backup, or run beta.21 on partially converted live data.

## Rollback

The retained backup may contain credentials (directories 0700, files 0600).
It is a node-generation backup, not a scheduled portable archive. Keep every
node backup as one stopped cluster generation. Preserve upgraded data before
restoring anything.

Stop all nodes. Restore each complete `backup/data/` to its original data path;
restore any `backup/controller/` to the external path recorded in `state.json`.
Restore matching original configurations and beta.21 binaries on all nodes
before starting any node. Restore archive-only repository mounts separately if
needed. Restoring only `slotmeta`, or starting beta.21 directly on format 2,
is unsupported. Messages accepted after the backup are absent from rollback.

## Acceptance test

Build the unchanged `v3.0.0-beta.21` checkout and candidate, then run:

```sh
WK_E2E_FORMAT1_BINARY=/absolute/beta21/wukongim \
WK_E2E_BINARY=/absolute/candidate/wukongim \
WK_E2E_FORMAT_UPGRADE_REPORT=/absolute/upgrade-receipt.json \
GOWORK=off go test -tags=e2e ./test/e2e/migration/format_upgrade \
  -count=1 -timeout=15m -p=1 -v
```

The three-node cluster uses 256 Hash Slots, 12 physical Slots and three replicas.
It creates more than 128 legacy metadata records so interruption exercises a
partial batch, proves old snapshot installation, and compares complete public
history at every ingress. It authenticates with an
original credential, preserves UID bans including an absent user's ban, sends
through WKProto, restarts all nodes, appends again, stops the observed Channel
Leader, proves a different Leader and follower catch-up, then boots beta.21 from the
backups and checks the complete original history. The JSON receipt binds both
binaries and every backup file. Without the explicit historical binary, ordinary
E2E discovery skips this scenario.

In a temporary source copy only, enable gofail on `pkg/db` and `pkg/db/meta` and
build a fault candidate. Set `WK_E2E_FORMAT_UPGRADE_FAULTS=1` to also SIGKILL
startup while copying the backup, after a durable metadata batch and immediately
before format publication. Each case checks exclusive ownership and resumed
history. The last also proves backup corruption prevents publication. These
controls are absent from ordinary delivered binaries.
