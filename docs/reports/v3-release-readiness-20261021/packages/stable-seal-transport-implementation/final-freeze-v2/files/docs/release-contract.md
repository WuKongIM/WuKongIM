# Native package release contract

## Source identity

Only a strict SemVer tag whose commit is reachable from
`WuKongIM/WuKongIM@main` may enter the repository. A dispatch payload is only a
hint: the target workflow must re-read the tag, release, source commit, asset
set, checksums, attestations, and immutable-release state from GitHub.

The source repository remains responsible for building and testing the deb/rpm
payload without signing credentials. Signing jobs treat those artifacts as
data and never execute files from them.

## Reviewed control plane

`manifests/channels.json` fixes the source repository, preview releases,
capacity bounds, and any in-progress retirement. `manifests/preview-signing.json`
fixes the protected Environment, public-key paths, fingerprints, secret names,
and key-lifetime policy. `manifests/bootstrap-packages.json` fixes whether the
repository bootstrap packages are enabled and their strict release SemVer.
`manifests/source-read.json` keeps cross-repository
access disabled until the private `WuKongIM Source Release Reader` GitHub App
has been installed only on `WuKongIM/WuKongIM` with Contents and Attestations
read access. Its client ID and private key exist only in the protected
`native-package-source-read` Environment as `WK_SOURCE_READ_APP_CLIENT_ID` and
`WK_SOURCE_READ_APP_PRIVATE_KEY`. These manifests reject unknown fields and
duplicate JSON keys. Workflow inputs, repository variables, dispatch payloads,
and release titles must not override reviewed values.

`manifests/audit-access.json` fixes the two package App Environments, secret
names, installation owner and repository, and exact permissions. It remains
disabled until both Apps and their isolated protected Environments have been
provisioned. Every package audit reader or writer job validates the complete
control plane and requires this manifest to be enabled before minting an App
token. The initial control change therefore remains fail-closed with
`enabled: false`; enabling access is a separate reviewed control change after
external provisioning succeeds.

GitHub's Immutable Releases status endpoint requires repository
Administration read access, which the workflow `GITHUB_TOKEN` cannot request.
The private `WuKongIM Package Policy Reader` GitHub App is installed only on
`WuKongIM/packages` with Administration read permission only; GitHub supplies
the implicit Metadata read permission. Its client ID and private key exist
only in the protected
`native-package-preview-audit-read` Environment as
`WK_PACKAGE_AUDIT_READER_APP_CLIENT_ID` and
`WK_PACKAGE_AUDIT_READER_APP_PRIVATE_KEY`. Only the publisher policy and
immutable replay jobs use this App, and only for the Immutable Releases policy
endpoint. Published Release, tag, and asset reads use those jobs' ordinary
read-only workflow tokens instead.

The separate private `WuKongIM Package Publisher` GitHub App is installed only
on `WuKongIM/packages` with Administration read and Contents write permissions.
Its client ID and private key exist only in the protected
`native-package-preview-audit` Environment as
`WK_PACKAGE_PUBLISHER_APP_CLIENT_ID` and
`WK_PACKAGE_PUBLISHER_APP_PRIVATE_KEY`. Only draft creation, audit-tag binding,
numeric draft classification, and sealing use this App. GitHub exposes draft
Releases only to push-capable identities and offers no installation-token
permission that is both read-only and draft-capable. The classification job
therefore requests Contents write but injects the token only into an exact
numeric GET/download resolver; the executable workflow contract forbids that
job from using POST, PATCH, PUT, DELETE, Release upload/edit commands, or Git
push. It does not request Administration permission. Draft creation,
audit-tag binding, and sealing retain the exact Administration read and Contents
write permissions needed for their policy checks and bounded mutations. The
three Apps use three distinct private keys; a key must never be copied into
another App's Environment. None of the Apps has webhook, OAuth, workflow,
secret, environment, or organization permission. The ordinary workflow token
remains read-only in these jobs.

The checked-in `site/` tree always remains the disabled bootstrap page. Live
APT/RPM content is generated from reviewed control plus immutable Releases,
verified as a complete snapshot, and sent directly as one Pages artifact. It
is not committed to `main` or a generated branch.

The production repository builder and two supplemental legacy verifiers are
loaded only from the exact source commit and file digests recorded in
`manifests/trusted-toolchain.json`. The TEST ONLY integration drill uses the
other files in that same reviewed manifest, but they are not copied into the
production tool boundary. Production signing, snapshot composition, audit
receipt validation, and final cryptographic verification come from this
repository's exact protected control commit and execute inside the separately
digest-pinned, GitHub-attested signing image recorded by
`manifests/signing-toolchain.json`. The source Release tag and its assets
cannot choose executable publishing logic. Updating either pin is an ordinary
protected control-plane review.

## Source preflight

The read-only source preflight accepts a numeric Release ID, never a release
name or download URL. It requires a published immutable source Release with the
exact four archives, unsigned amd64 DEB and RPM, and checksum asset. It peels
the Git tag, proves the source commit remains reachable from `main`, downloads
each asset by numeric asset ID, checks API and local SHA-256 values plus the
checksum closure, then repeats the Release, tag, and ancestry checks after the
downloads.

The hosted preflight accepts an expected `source_channel` of `preview` or
`stable`, and passes it unchanged to source resolution, attestation verification,
and metadata inspection. The three standalone commands also accept explicit
`--channel stable` for read-only source preparation. Their default remains
`preview`: it requires a SemVer prerelease and `prerelease: true`. Stable
inspection requires a strict version without a prerelease suffix and
`prerelease: false`. All three commands reject build metadata, invalid numeric
identifiers, and inconsistent source classification. The source-resolution
receipt retains its existing schema with the exact tag, version, and
classification; selecting a source channel does not authorize publishing it.

Stable preparation must pass `--channel stable` to
`scripts/resolve-source-release.py`, `scripts/verify-source-attestations.py`,
and `scripts/verify-source-package-metadata.py`. It retains the same numeric
Release ID, immutable identity, seven-asset checksum closure, main ancestry,
exact source SHA, and fixed signer-workflow checks. Selecting an expected source
classification cannot alter the reviewed publication controls. The stable
channel, signing, storage, bootstrap and publication gates remain disabled
until their separate implementation and provisioning are complete.

Metadata inspection validates the complete source receipt and all seven asset
bytes before and after native queries. Stable package versions are exactly
`X.Y.Z`; preview DEB versions are `X.Y.Z~prerelease`, with remaining hyphens
changed to underscores for RPM. Both families must be named `wukongim` with
amd64/x86_64 architecture. RPM requires release `1`, epoch `0` (including an
unset epoch), and no SIGPGP, SIGGPG, RSAHEADER or DSAHEADER signature. Native
queries run with a fresh home and without inherited credentials. The v2
metadata receipt binds the expected channel, numeric source Release ID, tag,
source SHA, and each package's asset ID, digest and size. It is metadata evidence,
not an independent proof of GitHub provenance; the preceding attestation step
remains mandatory for a successful hosted preflight.

The TEST ONLY PR drill builds tiny data-only stable and preview DEB/RPM fixtures
inside the existing pinned toolchain with networking disabled. It uses a
scratch-only ephemeral RSA key to exercise actual RSAHEADER rejection; fixture
packages are never installed or executed, and private test material is never
uploaded. Native evidence names the signature tags actually observed. Separate
CLI tests reject each legacy/header signature field independently; they do not
claim all historical signing formats were produced by the current native tools.

After those checks succeed, the resolver seals the public source assets as
read-only files in a read-and-traverse-only directory (`0444` files and a
`0555` directory). This preserves immutability while allowing the separately
UID-mapped, non-root signing-toolchain container to inspect package metadata
through a read-only bind mount.

All seven downloaded files must also verify against GitHub provenance signed
by `.github/workflows/binary-release-publish.yml` at the exact source tag and
commit on GitHub-hosted runners. DEB/RPM inspection is metadata-only: target
automation never runs a binary or maintainer script taken from the source
Release. For an `add_release` publication, the exact eight-file evidence set
(seven per-asset attestations plus its canonical summary) is carried through
the publisher and preserved below `audit/source-attestations/` in the immutable
package snapshot. The manual preflight remains read-only and does not publish
or sign anything.

## Channels and retention

The intended public layouts are:

- `/repo` for the stable, generated POSIX `sh` repository-setup entrypoint;
- `/apt/dists/preview` and `/rpm/preview/el/9/x86_64` for pre-releases on
  GitHub Pages;
- `/apt/dists/stable` and `/rpm/stable/el/9/x86_64` for stable releases only
  after publication has migrated to object storage and a CDN.

GitHub Pages documents a 1 GB published-site limit. The preview publisher keeps
at most four amd64 versions online and treats 750 MiB as a mandatory migration
threshold rather than consuming the remaining platform capacity. It emits an
operator warning at 600 MiB. Stable
publication is never enabled on Pages. Release assets remain immutable audit
evidence even after a version ages out of the installable window. Adding
another architecture requires a new capacity decision before changing this
limit.

An `add_release` target must rank strictly above every active base version under
strict SemVer precedence and under the exact DEB and RPM version mappings used
by the fixed signing toolchain. Snapshot release facts preserve the reviewed
manifest order; canonical payload inventories sort their version keys lexically.
The public verifier checks unique release identities and exact payload coverage
without imposing lexical order on release facts. The planner calls both `dpkg --compare-versions`
and RPM's native `rpm.vercmp`; disagreement or a non-increase fails closed. A
`remove_indexes` transition may remove only the oldest active version. For an
addition, all four clean-client checks must download bytes mapped exclusively
to the reviewed target version, so successfully refreshing metadata without
selecting the new package is not publication success.

An `update_bootstrap` publication changes no product release, payload, or
retirement state. It requires an immutable base snapshot and names one current
active product version only so clean-client validation has an exact install
target. The bootstrap package version must increase whenever either embedded
public certificate or repository definition changes. Repeating any other
publication preserves the exact existing bootstrap package bytes.

Bootstrap version `1.1.0` is the publication boundary that first adds the
unified `/repo` entrypoint. It therefore uses `update_bootstrap` against the
immutable `1.0.0` base without changing product releases. Only the exact
published `1.0.0` audit identity may be reverified without `/repo`; every new
snapshot must contain it.

Retirement is a two-run operation because package and metadata objects can
remain in the Pages CDN cache for roughly ten minutes. The first reviewed run
removes the oldest version from APT and RPM indexes while retaining its payload
and dependency closure, then records an `indexes_removed` state and an absolute
`not_before` at least thirty minutes later. A separate run after that timestamp
may remove the bytes and clear the retirement state. A workflow never sleeps
through the cache interval.

## Signing

APT and RPM use separate OpenPGP certificates for each channel. APT signs the
same `Release` bytes as both `InRelease` and `Release.gpg`; individual deb files
do not receive a non-standard signature. RPM packages are signed before
`createrepo_c` builds metadata, and the same channel RPM certificate signs
`repodata/repomd.xml`.

After it is provisioned, preview CI may import only the encrypted current,
sign-only subkey for that family. APT uses the protected
`native-package-preview-apt-signing` Environment and the
`WK_APT_PREVIEW_SECRET_SUBKEY_B64` / `WK_APT_PREVIEW_PASSPHRASE` secrets. RPM
uses `native-package-preview-rpm-signing` and the
`WK_RPM_PREVIEW_SECRET_SUBKEY_B64` / `WK_RPM_PREVIEW_PASSPHRASE` secrets. The
two operator-retained certify-only primary keys must never be present in either
environment.

Each reviewed certificate contains one certify-only primary and an exact
`current`, `next`, and `historical` signing-subkey set. Only `current` private
material may enter CI. `next` is public-only, may have a future creation time,
and must extend the current subkey's expiry by at least the 45-day rotation
runway. `historical` contains former current sign-only subkeys, which may still
be valid, expired, or revoked, but must be public-only in CI, must not expire
after the current subkey, and must never be disabled, invalid, or future-created.
An expired or revoked historical key remains audit topology only and cannot
authorize any deployable payload. A currently usable historical key can
authorize only byte-for-byte preserved RPM payloads. New RPMs and all newly generated
repository metadata remain restricted to the current subkey. Every listed APT and RPM fingerprint is
globally distinct. Their trailing 16-hex and 8-hex key IDs are also globally
distinct because RPM verification output may identify a signer by a shortened
key ID. Subkeys last at most 180 days, and the current subkey must have at least
30 days remaining.

The signed APT `Release` header set is closed to the exact source-builder
output: `Origin`, `Label`, `Suite`, `Codename`, `Architectures`, `Components`,
`Acquire-By-Hash`, a canonical UTC `Date`, and the four standard checksum
sections. The verifier rejects additional client-policy headers, including
`NotAutomatic`, `ButAutomaticUpgrades`, `Valid-Until`, and `Signed-By`, and
rejects a `Date` more than 60 seconds in the future. There is intentionally no
maximum age because an immutable audited snapshot must remain reproducible.

Because an RPM signature changes the package bytes, an old package is never
silently re-signed during rotation. Operators must complete the normal
`remove_indexes` then `remove_payloads` retirement before its actual signing
subkey expires or is revoked. The publisher independently isolates each
candidate signing subkey in its own RPM database and requires exactly one
currently usable reviewed key to verify; the packet's hashed issuer fingerprint
must agree with that cryptographic result and is not trusted as identity by
itself.

Every RPM-family primary, current, next, and historical key is RSA: its GnuPG
colon record must report public-key algorithm `1`, and its key size must be
exactly 3072 or 4096 bits. This RSA restriction does not apply to the APT
family. The control-plane certificate check enforces the RPM rule before any
signing job starts, and the isolated signer repeats it against both the public
topology and imported secret-key records before and after secret-subkey import.

Every RPM package-header signature and `repomd.xml` signature must use
SHA-256. New RPM payload signing fixes rpmsign's `_gpg_digest_algo` to `sha256`;
repository-metadata signing fixes GnuPG's digest to `SHA256`; and the isolated
signer proof rejects a `VALIDSIG` status whose OpenPGP hash algorithm is not
`8` (SHA-256). Verification of preserved signed RPMs likewise rejects a
header-signature packet that declares any other digest algorithm.

Before any signing command, each encrypted secret-subkey export is independently
loaded into a fresh isolated GnuPG home. Validation rejects a private primary,
an extra or token-backed private subkey, an unprotected subkey, the wrong
passphrase, a public-key topology change, a subkey lifetime over 180 days, or
less than 30 days of remaining validity. A proof signature must use the exact
reviewed signing-subkey fingerprint and SHA-256.

The manual signing-material preflight verifies the two GitHub Environment
secret pairs without creating or uploading any package, artifact, tag,
Release, or Pages snapshot. Its APT and RPM jobs remain separate, require the
exact current protected `main` commit, verify the digest-pinned toolchain's
GitHub provenance before referencing either family secret, and run the same
isolated proof-signature validator with networking disabled. The validation
output contains public certificate metadata only and is discarded rather than
stored or uploaded. Success proves only that, at that run, each Environment's
encrypted current subkey and passphrase match the protected manifest and public
certificate and can create a SHA-256 proof signature. CI does not validate the
operator-retained primary or next private key, revocation certificate, or
backup recoverability.

The public certificates are part of every composed snapshot and audit archive,
and are published at `/keys/apt-preview.asc` and `/keys/rpm-preview.asc`. The
minimal rotation keeps the same family primary: the old current moves to
`historical`, the pre-distributed next becomes current, and the
operator-retained primary certifies a new next. This promotion may happen at
the 45-day rotation boundary without waiting for expiry or revoking a healthy
former current. The next subkey must be present in the public certificate
before the first signed publication. The APT `wukongim-archive-keyring` and RPM
`wukongim-release` packages carry the complete reviewed family certificate, so
installed clients receive later certificate additions through normal
authenticated package upgrades before a successor is promoted.

## Repository bootstrap packages

The APT bootstrap package is architecture `all`. It owns only
`/usr/share/keyrings/wukongim-archive-keyring.pgp` and the Deb822 source
`/etc/apt/sources.list.d/wukongim-preview.sources`, whose `Signed-By` points to
that dedicated keyring. It never uses `apt-key`, the global trust store,
`Trusted: yes`, or maintainer scripts.

The RPM bootstrap package is `noarch`. It owns only
`/etc/pki/rpm-gpg/RPM-GPG-KEY-wukongim-preview` and `%config(noreplace)`
`/etc/yum.repos.d/wukongim-preview.repo`. The repository fixes the HTTPS EL9
x86_64 base URL, enables both package and repository-metadata signature
checking, keeps TLS verification enabled, and limits packages to `wukongim`
and `wukongim-release`. It contains no scriptlets.

Both bootstrap packages are built offline from reviewed control and public
certificates. The APT bytes are copied unchanged; a new RPM is signed only in
the RPM Environment. Each package is present in its signed repository index,
and the composer creates a byte-identical convenience download below
`/bootstrap/` only after signing. The canonical `/bootstrap/manifest.json`
records source and published SHA-256 values, sizes, paths, version,
architecture, and whether the publication introduced new bytes. The first
direct download is authenticated by the HTTPS origin; it cannot bootstrap
trust in itself. Subsequent updates are authenticated by the installed
package-manager trust root.

The composer generates `/repo` as fixed POSIX `sh` bytes from the canonical
HTTPS origin, both bootstrap download paths and published SHA-256 values, and
the reviewed RPM certificate SHA-256. It supports Debian and Ubuntu on amd64,
plus RHEL, Rocky Linux, and AlmaLinux 9 on x86_64. The entrypoint installs only
the matching data-only bootstrap package; it never refreshes an index, installs
`wukongim`, starts a service, or enables access to unrelated DNF repositories.
It uses a private temporary directory, removes it on normal or signal exit, and
refuses non-root execution with the exact public `sudo sh` command. All
mutations live inside a `main` function whose invocation is the final line, so
a response truncated before that complete invocation arrives does not enter
the setup path.

Offline site verification reconstructs and byte-compares `/repo` before
accepting the complete file closure. Clean-client validation repeats that
comparison for local artifacts and the public endpoint, then executes the
entrypoint twice on one pinned APT client and one pinned EL9 DNF client with a
network-disabled, exact-input curl shim. Both executions must be idempotent,
must install the bootstrap package, and must leave `wukongim` absent.

### Unpublished stable bootstrap preparation

`build-repository-bootstrap-packages.py` defaults to preview. Its original
manifest/certificate flags, package identities, data bytes and canonical
`wukongim.native_package_bootstrap_inventory/v1` output remain unchanged.
Stable preparation instead requires `--channel stable --control-root <checkout>`
and derives all inputs from that reviewed root: the existing
`bootstrap-packages.json` version, `stable-public-keys.json` roles, and fixed
`keys/apt-stable.asc` / `keys/rpm-stable.asc` files. It rejects explicit
manifest/certificate overrides, missing or partial registration, enabled stable
control, certificate substitution, and certificate changes during validation.
The existing public validator enforces topology, lifetime and full/16/8-hex
identifier uniqueness across the four APT/RPM channel certificates. Executable
validation code comes from the builder checkout, never the input control root.

The stable packages are `wukongim-archive-keyring-stable` (`all`) and
`wukongim-release-stable` (`noarch`). They retain the data-only/no-script
contract and own separate files:

- `/usr/share/keyrings/wukongim-archive-keyring-stable.pgp` and
  `/etc/apt/sources.list.d/wukongim-stable.sources`, using APT suite `stable`
  and the dedicated `Signed-By` keyring;
- `/etc/pki/rpm-gpg/RPM-GPG-KEY-wukongim-stable` and
  `/etc/yum.repos.d/wukongim-stable.repo`, using only
  `https://packages.githubim.com/rpm/stable/el/9/x86_64/mirrorlist`, with no
  floating `baseurl` fallback and all signature/TLS checks enabled.

Their normal source configuration is enabled inside the generated packages;
this does not activate a server-side channel. Preparation must never be
advertised as an install command for the public service. Both packages remain
unsigned build outputs, and the inventory schema is
`wukongim.stable_bootstrap_preparation/v1` with `channel: stable` and
`published: false`. Existing preview composer, snapshot/audit validators and
client gates reject it; the current `/repo` installer is unchanged. The generic
family signer can operate on explicit repository trees but does not consume
this preparation inventory or authorize stable publication.

The native TEST ONLY drill builds both channels twice in the existing pinned
Linux toolchain, compares default/explicit preview and repeated stable bytes,
then uses real `dpkg` and `rpm` scratch databases to install both orders and
reinstall each package. It checks retained file bytes/ownership, exact package
identities, no product installation, and preserves a six-case public JSON result.
For native `--root` installation only, the network-disabled, read-only container
runs as root with only `SYS_CHROOT`; production signing remains non-root. No
package is installed on the host and no signature, dependency, or file-conflict
check is disabled. Only the JSON receipt leaves temporary scratch.

This proves package construction/coinstallation when the native drill passes.
It does not prove RPM package signatures, live APT/DNF repository behavior,
mirrorlist correctness, CDN A/B/cache transitions, or authenticated public
installation. Those require subsequent complete stable composition, signing,
immutable audit and public-client work. The separate existing local-signing drill
provides TEST ONLY signature coverage, not signatures on these preparation bytes.
The stable channel remains disabled throughout this slice.

Preview primary-key generation currently uses an explicitly accepted
single-operator online-workstation custody model. The named operator is
`tangtaoit`. The operator retains one encrypted full-secret backup and its
revocation certificate outside the repository in a mode-0700 local directory
with mode-0600 files, stores each distinct passphrase in the operating-system
credential store, and completes a restore check before CI secrets are
provisioned. Only the encrypted current sign-only subkey is copied to its
package-family Environment; the primary and next private keys never enter
GitHub. This lower-assurance model does not claim resistance to compromise of
the generation workstation. Moving preview signing to an offline or
multi-custodian trust model requires generating new primary certificates under
that model and distributing their public certificates; an online-generated
primary cannot become retroactively offline.

Stable private keys never enter GitHub-hosted CI. Stable signing uses an
operator-managed local Mac under the explicitly accepted online-workstation
custody model. Operator-retained encrypted backups may be stored on the Mac or
NAS. This reuses the encrypted-backup, local permissions, distinct passphrase,
operating-system credential-store, and restore-check practices described above;
it does not require or claim an offline or hardware-backed ceremony. Stable
certificates remain independent of preview and of the other package family.
Promoting an RPM preserves the same unsigned payload, NEVRA, and provenance,
but its bytes change when it is signed with the independent stable key. This
custody choice does not enable the stable channel or authorize publication.

The optional `manifests/stable-public-keys.json` declaration registers only the
two independent stable public certificates and their exact signing roles. It
reuses public-certificate validation and the existing lifetime policy, with
fingerprint and short-key-ID uniqueness across preview and stable. The complete
declaration and both certificates must be present together. Older controls
without any stable registration remain valid. Registration changes neither the
disabled stable channel nor the preview manifest, snapshot, archive or bootstrap
certificate inputs; it creates no GitHub secret or signing Environment.

The credential-free TEST ONLY PR drill also exercises the local file and
existing environment inputs with tiny data-only packages and independent
ephemeral APT/RPM keys, plus the stdin subkey option. After signing, it removes
test secret material before verifying both APT signatures, actual RPM package
signatures, repomd signatures and their package hash closure in fresh public-only
homes/databases. Wrong passphrases, full-primary signing input and tampered
outputs must fail. Only the public JSON result is uploaded; keys, packages and
signed fixture trees remain in temporary scratch. This is native input-adapter
regression evidence, not production key provisioning or stable publication.

## Atomic publication and recovery

Every run materializes a complete snapshot in a temporary directory, verifies
APT signatures, RPM package signatures, repodata signatures, metadata hashes,
and clean-client repository update plus authenticated download, then uploads
one Pages artifact. Production target automation never installs or runs a
source Release payload. The credential-free TEST ONLY drill may install the
packages it builds from the immutable trusted-tool commit. There is no in-place
mutation of the live repository.

GitHub propagates a skipped job through its dependency chain. Jobs that follow
an intentionally optional branch therefore use `always()` together with
explicit success checks for every required direct dependency; `always()` alone
must never convert a failed or cancelled prerequisite into a signing or
deployment attempt.

Canonical snapshot archives use `0755` directories and `0644` files. After
final archive validation, extraction also normalizes its root directory to
`0755`; the composer does the same immediately before atomically exporting a
completed snapshot. This makes the verified tree readable across the host
runner and the fixed non-root signing-container UID while leaving every
container consumption mount read-only. A failed mode transition aborts before
publication and triggers cleanup of the unpublished output.

The first `add_release` deployment accepts only the exact four-field v1
provisioned bootstrap status with both package families disabled and reason
`awaiting_first_release`. A stale `signing_not_provisioned` bootstrap is not a
valid predecessor for production publication; operators must first complete
the reviewed signing-provisioning bootstrap deployment.

Exact release assets are never overwritten. A retry may finish an exact
matching draft or redeploy an already verified immutable snapshot. A mutable
published release, a digest conflict, unexpected assets, or an unverifiable
source identity fails closed.

Audit Releases use an explicit two-control state machine. The preparation run
creates one canonical empty draft and reports its numeric ID while its final
non-version tag is still absent. A protected control change records that ID;
the bind run then creates the lightweight
`native-package-preview-r<ID>` tag once at that exact control commit and changes
only the empty draft's `target_commitish`. GitHub may detach a draft to a
generated `untagged-<20hex>` identity when the real tag is materialized. The
binder accepts that intermediate identity only while the Release remains the
exact empty draft and the canonical lightweight tag already points to the
reviewed control commit. The generated name must have no corresponding Git ref
and must remain byte-for-byte stable across recovery reads; the binder then
restores only the canonical `tag_name` and revalidates both objects. The
publisher accepts only the exact reserved tag and
numeric ID. The tag namespace must forbid deletion and force updates. Two
active, aggregating tag rulesets must match
`refs/tags/native-package-preview-r*`: a creation-only ruleset grants its sole
bypass to the numeric Integration ID of the Package Publisher App, while an
immutability ruleset denies update, deletion, and non-fast-forward operations
without any bypass actor. The rules stay separate so the App that creates a
reserved tag can never bypass its immutability. If protected `main` advances
after binding, that draft/tag pair is abandoned and a new numeric draft must be
prepared and reviewed; it is never rebound to a later control commit.

Repository immutable Releases must remain enabled. Immediately before making
a draft immutable, the writer re-downloads both canonical assets, re-reads the
exact two-asset draft state, and re-fences protected `main` plus the create-only
audit tag. It also re-reads the repository immutable-Release policy immediately
before and after the publication write. The publish response and final ID-bound
downloads are verified again. GitHub does not offer an atomic conditional
Release PATCH, so an administrator changing this policy inside the final API
race can still burn the reserved audit ID; the strict response check prevents
that mutable Release from reaching Pages. These checks detect interference;
they never delete, replace, or rename remote assets.

Each successful publication also creates an immutable Release in this
repository containing the exact signed snapshot inputs and audit receipt. That
Release, rather than Git history or the live CDN, is the durable rebuild and
rollback source.

GitHub Pages does not expose a conditional deploy primitive that atomically
asserts `main == control_sha`. The publisher fences `main` immediately before
and after deployment and again during public verification, so a concurrent
control change is detected, but a stale reviewed snapshot could be visible
briefly inside that platform race window. Operators must not merge a package
control change while any draft, bind, publisher, or bootstrap deployment is in
progress. Pull-request validation uses a separate concurrency group and cannot
replace a queued production run.

Suspected key compromise freezes publication. Using the operator-retained
family primary, maintainers revoke the affected subkey, promote the already
distributed next subkey, certify and publish a new next, publish an incident
notice, and remove affected versions from indexes or rebuild signed bytes as
required. A preserved
RPM whose issuer is revoked must fail verification and cannot remain published
under the normal historical-key exception. The
updated certificate, manifest, snapshot, and audit receipt must agree before
publication resumes. Audit artifacts remain retained.

## Beta.12 immutable public recovery

Audit `385374689`, bound to control
`785af29d5bb32d3996e3060b496a68dacecee78a`, was sealed and deployed before
the public verifier incorrectly rejected the reviewed release order. Recovery
uses the read-only immutable-public-reverify workflow against this exact audit.
The reviewed channel manifest is restored byte-for-byte to that artifact's
control. Audit `385393510` remains immutable but was never deployed; it is not
the active public identity. No Release, tag, signed payload, or Pages artifact
is modified by recovery.

The recovery boundary pins the numeric audit and artifact control, requires
all identity manifests and certificates to remain byte-identical, and permits
only the enumerated verifier, workflow, tests, and documentation differences.
It verifies archived signatures and provenance, compares public critical bytes
including `/repo` and the bootstrap manifest, and runs all four public clients
for exact version `3.0.0-beta.12`. Any other artifact or control-plane change
requires a separately reviewed recovery boundary.

## Installed CLI publication gate

Every new `add_release` publication must pass `--verify-installed-cli` in its
final public-client job. The existing download-only sandboxes still never
execute product payloads. Only after each public download matches the reviewed
snapshot does a separate, fresh container install those exact bytes. It receives
no credentials, Docker socket, host data directory, or writable control checkout.
The installation container has bounded memory, CPU, processes, file sizes and a
600-second deadline. Package-manager dependency downloads use the distribution's
normal repositories; RPM local-package signature checking stays enabled.

The reviewed acceptance script and checksum-bound synthetic fixtures are mounted
read-only. Commands run in private scratch space with caller WK_* and user
configuration removed, each with a 30-second deadline. Server and CLI identities
must both equal the snapshot's exact version, source SHA and `release` build
source. Functional checks cover `bench validate`, a bounded offline query for
one known user, and a complete `migrate diagnose` source scan. Invalid benchmark,
query and migration inputs must fail with their documented family exit codes.
Queries and diagnosis must preserve source bytes; diagnosis must not create the
target. No cluster, benchmark worker or message traffic is started.

A missing binary, wrong identity, incomplete result, changed fixture or source
bytes, timeout, or failure on any of the four distributions fails publication.
Deployment by itself remains incomplete. The receipt includes each installed
CLI result and the top-level `installed_cli_verified` gate. Bootstrap/retirement
operations keep their existing compatibility policy and do not require a CLI
from older retained releases.

`native-package-cli-acceptance.yml` is the read-only repeat check for the currently
reviewed published snapshot. Its only input is the exact current protected
control SHA. It derives the numeric audit and target from channels.json, resolves
the immutable archive/receipt, requires artifact identity manifests and keys to
match current control byte-for-byte, verifies public downloads and all installed
clients, and rechecks main. It shares the non-cancelling `packages-pages` group;
no control merge, publication, binding or snapshot replacement may race this run.
It neither signs nor deploys, creates Releases, or changes immutable audit tags.


## Stable first-generation offline candidate

The existing `plan-package-publication.py`, `prepare-package-site.py`,
`compose-package-site.py`, `package-audit-receipt.py` and
`verify-production-package-site.py` have an explicit candidate mode. Every
candidate invocation requires `--channel stable --offline-candidate
--control-root CONTROL --candidate-source-root SOURCE`; omission is an error,
not a schema-based fallback. Preview remains the default. The checked-in
stable channel must stay disabled with no releases and
`status=object_storage_required`. No production workflow or live site is changed.

This mode supports exactly the first `add_release` of one strict `X.Y.Z`
version. There is no base, retained/removed payload, key history, rotation,
bootstrap update, retirement, or prerelease/build-metadata version. The plan's
`audit_release_id`, `base_audit_release_id` and release `package_release_id`
are null. The candidate carries `stage=offline_candidate`, `channel=stable`
and an exact boolean `test_only`; the stage always means unpublished and
unsealed. Production consumers continue to require their original sealed
preview protocols and reject candidate inputs.

| Artifact | Candidate schema (explicit opt-in only) |
| --- | --- |
| Plan | `wukongim.native_package_publication_plan/v2` |
| Prepared product inventory | `wukongim.native_package_payload_inventory/v2` |
| Prepared/signed bootstrap inventory | `wukongim.native_package_bootstrap_inventory/v2` |
| Snapshot | `wukongim.native_package_snapshot/v4` |
| Composition result | `wukongim.native_package_site_composition/v2` |
| Independent verification | `wukongim/production-package-site-verification/v2` |
| External audit receipt | `wukongim/package-audit-receipt/v3` |

The public control root supplies the existing channels, preview signing,
stable public roles, bootstrap, trusted-toolchain and signing-toolchain
manifests plus all four public certificates. There is no additional signing
manifest, credential environment or exchange protocol. Stable current/next
roles come exclusively from `stable-public-keys.json`; full fingerprints and
16/8-hex key IDs must remain distinct across all four families. Actual public
certificate topology and current-key signatures are checked by the existing
bootstrap builder and independent verifier, not inferred from receipt strings.

For production input, `SOURCE` contains exactly `resolution.json` (the original
successful stable source-resolution receipt), `metadata.json` (the original
v2 stable native-metadata receipt), `assets/` (all seven original source
assets), and `attestations/` (the exact seven per-asset JSON files plus
`source-attestations.json`). The planner reuses strict stable source receipt
validation and binds every asset/evidence SHA-256 and size. Prepare repeats the
actual metadata-only DEB/RPM inspections against the seven bound assets. No
source package or maintainer script is executed. Source bytes are bounded by
750 MiB total, evidence JSON by 8 MiB, and public controls by 1 MiB per file;
the existing 750 MiB site and 64 MiB decompression limits remain in force.
Use canonical absolute paths. Control/source roots reject linked parent components or
hardlinked/special files; outputs must be new and outside all input roots.

`--control-sha` is required for a production candidate. It is the operator's
**declared** reviewed control revision, not a new proof of a Git tree or online
GitHub provenance. The independently supplied plan and the exact control,
source and local producer file bytes are the offline trust anchors. Every
stage rebuilds that plan against those external bytes; the verifier requires
an external `--plan` and never trusts the embedded copy as its own anchor.
The archive retains the original public preflight receipts/evidence, control
files and signing receipts. Binding their bytes does not rerun GitHub
attestation verification or authenticate a current GitHub Release. Future
sealing/publication must repeat the established source and immutable-identity
checks; local candidate success makes no public-deployment claim.

The TEST ONLY branch additionally requires `--test-only`. Its source root has
only `resolution.json` and two tiny native assets. The resolution schema is
`wukongim/test-source-fixture/v1`, with fields `version`, `source_sha:null`,
`release_id:null`, and exactly two assets `{name,id:null,size,sha256}` named
`wukongim_X.Y.Z_linux_amd64.deb` and `.rpm`. Scratch control revision is null:
do not pass `--control-sha`. This branch does not invent GitHub asset IDs,
commits, attestation claims or a source metadata receipt. Production input
rejects it. TEST ONLY private material is removed before independent archive
verification, and only bounded public JSON evidence may leave scratch.

The local sequence uses the original CLI arguments plus the common candidate
flags above:

1. Plan with `--channels CONTROL/manifests/channels.json`; save canonical
   stdout as `PLAN`. Production also supplies its declared `--control-sha`.
2. Prepare with `--plan PLAN --source-assets SOURCE/assets`, the fixed control
   bootstrap/certificate paths, and the trusted builder whose bytes match
   `trusted-toolchain.json`. Use this checkout's bootstrap builder, not code
   from the control root. Save the product and bootstrap inventories; the
   unsigned trees are `OUTPUT/apt` and `OUTPUT/rpm`.
3. Invoke the unchanged family signer explicitly. APT uses
   `--apt-release dists/stable/Release`; RPM uses
   `--rpm-repository stable/el/9/x86_64`. Its three canonical v1 allowlists
   name both product and stable bootstrap packages: all are new, none are
   preserved, and both are active. Select local secret inputs separately;
   candidate commands never locate or read production private keys.
4. Compose with the external plan, prepared inventories, signed family trees
   and signing receipts. `--signing` means the fixed
   `CONTROL/manifests/stable-public-keys.json` in this explicit mode. The
   composer itself copies the closed public audit evidence set. The result
   contains `site/` and `audit/`, including its embedded plan and snapshot.
5. Independently verify with `--plan PLAN --site-root OUTPUT/site
   --snapshot OUTPUT/audit/snapshot.json` and the same external controls and
   source root. This executes actual current-key/SHA256 APT and RPM checks,
   exact package identities and bootstrap contents, APT cleartext/detached
   Release/index/gzip/by-hash closure, and RPM signed payload/three metadata
   compressed/open hash closure. Signed bootstrap downloads must be
   byte-identical to their indexed packages.
6. Use the existing canonical archiver. The archive basename is
   `wukongim-stable-X.Y.Z-SNAPSHOT_SHA256-candidate-site.tar`, with the actual
   SHA-256 of `audit/snapshot.json`; the external receipt replaces `-site.tar`
   with `-receipt.json`. Audit `create`/`verify` receives the external plan,
   family receipts, control/source roots and archive. It extracts canonical
   members, compares every public evidence copy, and calls the independent
   verifier again. Extract and verify once more to test local recovery.
   Determinism means identical frozen signed inputs, not repeated signatures
   with different creation times.

The candidate `/repo` is clearly marked UNPUBLISHED and is generated for the
separate stable package names. This private candidate tree does not replace
public preview `/repo`. Its normal stable bootstrap configuration retains the
planned mirrorlist endpoint and has no floating baseurl fallback; that endpoint
is not created or contacted here. No claim is made about client installability,
atomic repository switching, caches, A/B routing or cloud storage.

A future audit seal must bind the **exact existing candidate archive digest**
from outside the archive, after independent source/signature verification.
Filling null IDs inside an existing snapshot would create different bytes and
must not be described as sealing the same artifact. This slice does not
implement that seal, upload/activation, production signing or public exact-version
APT/DNF acceptance. Those remain release completion requirements.

## Stable external audit seal and composite transport

Explicit stable bind/seal/readback preserves the original candidate pair and null IDs.
Sealing authenticates historical control/producer Git bytes and fresh source
provenance before uploading; normal stable endpoints are fixed to GitHub.com.
TEST ONLY loopback evidence remains separate. The explicit offline composite
planner retains both channels and all supplied generations without uploading or
activating. Stable remains disabled and no production workflow is connected.
See [the operator contract](stable-audit-transport.md) for identities, inputs and verification limits.
