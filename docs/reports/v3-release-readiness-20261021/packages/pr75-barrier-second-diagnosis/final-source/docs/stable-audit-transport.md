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
For the two already verified stable APT indexes, the composite planner retains
SHA512 aliases alongside the archived SHA256 paths so native clients can use
their default hash preference across a generation switch. Each additional alias
copies the original authenticated archive member; signed metadata, archives and
the preview-only planner are unchanged.

Stable RPM payloads are also retained under each supplied stable snapshot's RPM
base URL. DNF's separate `makecache --refresh` and download invocations can select
A's signed metadata, then reread the floating mirrorlist after it points to B.
The composite plan therefore adds explicit missing `Packages/*.rpm` aliases
under B's prefix for A's verified product and bootstrap bytes, and likewise for
the other supplied stable snapshots. Each alias binds an original archive member,
size and SHA-256; it does not create a new signed candidate or metadata file.
Original snapshot objects remain unchanged. Both original objects and aliases
participate in URI conflict checks: identical bytes can share a deterministic
source, while conflicting bytes fail closed, including a same-version bootstrap
that has been signed again. Reuse identical signed bytes or change its version.
The existing limit of eight total snapshots, including current preview, permits
at most seven stable snapshots; each verified stable candidate contains exactly
the product and bootstrap RPM, so at most 84 missing cross-generation aliases
are added. Only supplied stable generations participate; preview is unchanged.

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

## Stable native clients and retained generations

The package client validator has an explicit stable mode. Supply the reviewed
v2 composite transport inputs, both reviewed stable public certificates, the
canonical HTTPS origin, an exact release version, and `--verify-installed-cli`.
The host must provide `gpg`, `gpgv`, `rpm`, `rpmkeys`, `dpkg-deb`, and `zstd`.
It repeats the existing candidate signature and closure verification in the
same process; it does not trust a caller-supplied `verified` receipt. Tool
versions, the transport input and plan digests, the candidate, downloads and
installed CLI results are recorded separately from GitHub provenance.

Stable bootstrap inventory comes from the immutable stable snapshot, not the
preview root manifest or status. Native APT reads stable sources and by-hash;
native DNF reads the packaged mirrorlist with TLS, metadata and package
signature checks enabled. There is no baseurl fallback. Each download client
has a 300-second limit and each installed client retains its existing
600-second limit and full server/wkcli identity, bench, query and migration
checks. Download-only consistency tests do not pass the production stable gate.

The independent Linux TEST ONLY job builds real server and wkcli binaries from
fixed source `fc18221282f289088ec9b4282fda8e0bfc67a3b8`, using Go 1.25.11 and
explicit test versions 3.0.0/3.0.1. Those versions are test build parameters, not
source Release or attestation claims. A/B share disposable public keys but use
bootstrap 1.1.1/1.1.2, avoiding conflicting independently signed bytes under one
immutable URL. Production bootstrap controls remain unchanged. Test private
keys are destroyed before public fixture export and host verification.

The four digest-pinned Ubuntu, Debian, Rocky Linux and AlmaLinux download
clients use an internal Docker network and an ephemeral CA for the exact
`packages.githubim.com` name. A deterministic server switches after serving A's
InRelease or mirrorlist. APT retains A's authenticated by-hash indexes. The RPM
switch case sets `wukongim-stable.metadata_expire=-1` on both DNF invocations.
The first still runs signed `makecache --refresh` to authenticate fresh A; the
second reuses that selected metadata. DNF can persist an explicit expired state
when `makecache` exits under the packaged `metadata_expire=0` policy, causing the
next command to refresh to B even with its own expiry value set to -1. Holding
the same test policy across both phases prevents that teardown boundary from
changing the selected generation. No persisted state or metadata mtime is edited.
The explicit second phase keeps the floating mirrorlist and must record B's
exact planned mirrorlist response before an exact-A payload response under B's
planned prefix, within the same case. An A-only download or another retained
generation cannot satisfy this proof. Packaged configuration, production
validation, fresh-B clients and all persistent-cache/negative cases retain their
original metadata policy. DNF must read A's exact immutable metadata URLs and
bytes, including `repomd.xml` and its detached signature during the switch. The
B-prefix RPM path must resolve through the plan to the exact original member,
size and SHA-256 selected from A. Native signature, exact downloaded package and
installed version/source checks remain required. Unknown aliases, cross-family/architecture paths, mismatched
source members or bytes, and incomplete response writes fail the proof.
For cache cases, a metadata 304 must refer to the same immutable object, carry
its expected ETag and match the request's conditional validator. `If-None-Match`
takes precedence over the object's hash-derived `Last-Modified` value. Native
signature verification and independent cache-read evidence remain required.
The HTTPS fixture completes bounded TLS shutdown, including `close_notify`;
intentional truncation still fails the declared HTTP response length. APT faults
cover both supported by-hash algorithms without forcing a client hash choice.
Request logs expose the actual URLs, response digests and cache conditionals.
Separate cases cover persistent caches and missing/corrupt retained objects,
TLS rejection, redirect escape, interrupted responses, and exact-target/source
identity rejection. RPM cache cases persist the final CLI cache/state paths and
the earlier installroot paths in separate scratch directories: the pinned DNF
versions initialize their global state before reapplying command-line paths to
the repository cache. Inventory excludes only the five observed GPG IPC socket
names (`S.gpg-agent`, `S.gpg-agent.browser`, `S.gpg-agent.extra`,
`S.gpg-agent.ssh`, and `S.scdaemon`) directly in
`dnf-cli-cache/wukongim-stable-5702a8a51b8fa885/pubring`. This directory is fixed by the canonical
stable mirrorlist and pinned libdnf. Each excluded node must be an actual
single-link, zero-byte Unix socket; it is never read, connected, or deleted by
inventory. Other special files or locations still fail closed, with bounded
relative-path and stat diagnostics. Ordinary-file limits remain unchanged.
A state file or socket alone does not count as metadata. Repeated cache
acceptance still requires unchanged metadata bytes and inode plus an observed
read after timestamp arming. Installed CLI containers use ordinary
distro dependency networking and only the already authenticated local product package; that
traffic is not part of the isolated A/B proof.

The driver records and removes only its own container/network IDs. Controlled
failure, timeout and TERM attempt cleanup and publish the independent cleanup
result. Host loss or SIGKILL cannot guarantee a final receipt. Public artifacts
contain bounded JSON and request logs, not private keys or package trees.
These tests do not enable stable, create public Releases, activate storage, or
prove global CDN atomicity. Actual public-origin verification of the exact
published stable version remains a release requirement.
