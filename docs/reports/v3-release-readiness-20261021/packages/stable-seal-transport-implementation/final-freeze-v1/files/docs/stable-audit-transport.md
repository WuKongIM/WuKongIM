# Stable audit sealing and composite transport

Stable remains disabled and no publication workflow is enabled by these tools.
This layer preserves the exact offline candidate archive and v3 receipt, including
all null audit IDs and signed APT/RPM bytes. A separate immutable GitHub Release
binds the original two assets; there is no third signing or exchange artifact.

## Sealing identity

The explicit `--channel stable` mode uses the reserved tag
`native-package-stable-r<actual numeric audit Release ID>`. The audit container is
still a prerelease and is never made latest. This does not change the stable
classification of the source product Release. Archive/receipt names retain the
original version and snapshot SHA-256; names are never replaced with the audit ID.

`--expected-control-sha` identifies the current reviewed sealing checkout and
reserved tag. The candidate plan's `control_sha` identifies its original control
commit. They may differ. Stable binding/sealing verifies the actual historical
Git objects, all ten control files and twelve producer files, and ancestry to
fresh packages `main`. Current executable producer bytes must still match those
anchors; downloaded historical code is never executed.

Binding only reserves an empty draft/tag and does not assert source verification.
Before any asset upload, sealing reuses the existing numeric source resolver,
actual fixed-workflow GitHub attestation verifier, unsigned native metadata
inspector, and candidate audit/signature verifier. A saved receipt claiming these
checks succeeded is insufficient. Final publication rechecks source identity and
ancestry, plus the existing main/tag/immutable-policy fences. Failed attempts do
not overwrite or delete existing assets, and resumptions repeat verification.
Fresh observations do not rewrite the original candidate's source evidence.
The resolution/metadata bytes read for fresh verification must match the already
captured candidate plan; they are hashed and parsed from the same bounded read.

The existing bind/seal/resolve CLI defaults and preview schemas remain unchanged.
Stable binding supplies `--candidate-plan` and the original `--control-root`.
Resolution supplies the original `--candidate-plan` and `--candidate-receipt`.
Seal supplies `--candidate-plan`, `--control-root`, and additionally requires `--candidate-source-root`,
`--apt-receipt`, `--rpm-receipt`, `--archive`, and `--receipt`; the optional
`--source-token-env` names an existing read-only source credential. Source tokens
are not embedded in files or receipts. No App, environment or authentication is
created by the helper. Normal stable API/download origins are fixed to GitHub.com.

A production invocation requires an actual immutable stable source Release,
an already reviewed candidate, and the reviewed executable checkout/toolchain.
The external sealing-control SHA remains an operator-selected current-main fence;
the helper authenticates the candidate’s historical control/producer Git objects,
not its own entire checkout as a Git tree. The repository does not yet provide an enabled
production workflow or OSS/CDN activator. Implementation and TEST ONLY validation
of these helpers do not constitute a production seal or release.

## Offline composite plan

The planner requires explicit `--composite` with
`wukongim.object_storage_plan_inputs/v2`, `channel: composite`, both current
preview and stable descriptors, and an explicit retained-generation list. The
preview descriptor wraps the existing v1 snapshot descriptor. A stable descriptor
binds its independently supplied plan/control/source/receipt inputs, candidate
tuple, numeric audit ID, sealing control SHA, and external v2 resolution bytes.
Only an immutable complete observation is accepted. Every retained snapshot is
fully verified before combining inventories. At most eight snapshots and 2 GiB
of declared archives are accepted; actual archive sizes must match.

A locally supplied resolution JSON is evidence, not authenticated GitHub truth.
Plans remain `plan_only`; GitHub/control/cloud/activation verification flags remain
false. A future activator must fresh-resolve exact audit/source/control identities.
Stable signatures are reverified by the native verifier; the legacy preview
planner does not add a new preview signature-verification claim.

Every original site/audit object is retained below its channel-specific immutable
snapshot path. Current preview continues to own `/repo`, `/index.html`,
`/status.json`, `/signing-manifest.txt`, and `/bootstrap/manifest.json`. Stable does
not replace those shared entries, and no `/repo-stable` installer is emitted.
Stable APT/by-hash/payloads and versioned bootstrap downloads use canonical paths.
Conflicting bytes at the same public URI fail rather than selecting a winner.

The only derived object is a no-store stable RPM mirrorlist pointing to the
complete immutable snapshot RPM base URL. Its exact UTF-8 bytes, size and digest
are in `generated_objects`. No floating stable repomd/plain-metadata alias is
created; signed RPM repository bytes remain unchanged. All supplied generations
are retained; there is no garbage collection, deletion, activation, or promise
of an atomic CDN switch.

## TEST ONLY validation

Stable test mode requires `--test-only`, loopback API/download endpoints and
explicitly test-only candidate inputs. All redirects are rejected, including
loopback-to-loopback redirects; test clients also ignore proxy configuration. Test evidence retains null source IDs and is
rejected by normal composite mode. It never demonstrates live GitHub provenance.

The existing native candidate drill keeps its original 31 cases and additionally
runs `transport_cases` after destroying test secrets: loopback bind/seal/readback,
composite planning, deterministic repetition, retained-generation validation,
and rejection without the test opt-in. Any transport failure fails the whole drill. Its stable DEB/RPM signatures
are real native signatures; its preview transport fixture is explicitly
structural and is not reported as native preview signature acceptance.
