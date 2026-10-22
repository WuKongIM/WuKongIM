# WuKongIM Linux packages

This repository owns the public APT and RPM repository metadata for WuKongIM.
The intended public endpoint is `https://packages.githubim.com`.

The signed preview channel publishes amd64/x86_64 packages. Preview releases
are prereleases and should be validated outside production before rollout.

## Install with the package manager

On Debian or Ubuntu, use three steps: add the repository once, refresh the
package index, then install WuKongIM by package name. Future installs and
upgrades no longer require downloading a WuKongIM deb file manually:

```bash
# 1. Add the WuKongIM preview repository
curl -fsSL https://packages.githubim.com/repo | sudo sh

# 2. Refresh the package index
sudo apt update

# 3. Install WuKongIM
sudo apt install -y wukongim
```

On Rocky Linux, AlmaLinux, or RHEL 9, use the same three steps with DNF (or the
compatible `yum` command):

```bash
# 1. Add the WuKongIM preview repository
curl -fsSL https://packages.githubim.com/repo | sudo sh

# 2. Refresh the package index
sudo dnf -y --disablerepo='*' --enablerepo=wukongim-preview makecache --refresh

# 3. Install WuKongIM
sudo dnf install -y wukongim
```

The `/repo` entrypoint is POSIX `sh`. It detects Debian/Ubuntu amd64 or
RHEL/Rocky/AlmaLinux 9 x86_64, downloads the matching reviewed bootstrap
package, verifies its exact published SHA-256, and installs only that package.
It does not refresh package indexes or install WuKongIM. Temporary downloads
are removed on exit. The RPM path additionally verifies the reviewed public
certificate bytes and package signature while keeping every other DNF
repository disabled.

The first `/repo` download necessarily trusts the HTTPS origin. Its bytes and
the exact bootstrap package identities are closed over by the immutable audit
snapshot. The bootstrap package installs only the reviewed repository key and
dedicated source file; it does not start services or weaken signature checks.
After that first step, APT/DNF authenticates repository metadata and packages,
and upgrades the bootstrap package through the same signed repository.

The checked-in `site/` directory is deliberately only the disabled bootstrap
page. Once preview publishing is enabled, the publisher builds and verifies a
complete repository snapshot in temporary storage and deploys that artifact
directly. Generated package indexes, metadata, signatures, and payloads are
never committed to Git.

## Trust boundary

- `WuKongIM/WuKongIM` builds an exact tagged source revision without signing
  credentials.
- This repository independently verifies the immutable source release before
  accepting a publication request.
- Once provisioned, preview APT and RPM signing use separate certificates and
  separate protected GitHub Environments. Each certificate pre-distributes a
  current and a next short-lived signing subkey.
- Preview primary and next private keys currently use an explicitly accepted
  single-operator online-workstation custody model. CI still receives only the
  encrypted current sign-only subkey for the applicable package family.
- Stable signing uses operator-managed local Mac keys, with encrypted backups
  on the Mac or NAS. This accepted online-workstation custody model keeps
  stable private keys outside GitHub-hosted CI; stable publication remains
  disabled. This custody choice does not claim an offline ceremony.
- GitHub Pages will receive only complete, verified preview snapshots. Stable
  publication requires migration to object storage and a CDN.
- `manifests/channels.json`, `manifests/preview-signing.json`, and
  `manifests/bootstrap-packages.json` are the reviewed, fail-closed
  publication controls. GitHub variables and dispatch payloads are not trust
  anchors.
- `manifests/source-read.json` keeps cross-repository verification disabled
  until a source-only GitHub App and protected Environment are provisioned.
- `manifests/audit-access.json` keeps both package audit Apps fail-closed until
  their exact installations, isolated Environments, secrets, and permissions
  are provisioned and a separate reviewed change enables the manifest. Every
  reader or writer job checks it before minting an App token.
- Immutable Release policy reads and audit Release/tag writes use separate
  package-only GitHub Apps. The Immutable Policy Reader credentials live only
  in the protected read Environment and have no Contents permission; ordinary
  reads of published Releases, tags, and assets use the workflow token. GitHub
  exposes draft Releases only to push-capable identities, so the numeric draft
  classification job uses a repository-limited Publisher token while its
  reviewed workflow contract forbids every write operation. The Publisher
  credentials live only in the protected write Environment and are exposed to
  exactly draft creation, draft classification, audit-tag binding, and
  sealing. All three App private keys are distinct and remain isolated to
  their own Environment.
- `manifests/trusted-toolchain.json` pins the exact source commit and SHA-256
  bytes of the repository builder, supplemental legacy verifiers, and TEST
  ONLY drill. Production signing, snapshot composition, and final verification
  use this repository's exact reviewed control inside the separately
  digest-pinned and attested signing image. A source tag never selects
  executable publishing code.

The manual source preflight accepts only a numeric immutable source Release
ID. It independently resolves the tag to a commit reachable from `main`,
downloads all seven assets by numeric asset ID, verifies their exact checksum
closure and fixed-workflow GitHub attestations, and inspects DEB/RPM metadata
without executing either package payload.

The read-only source preflight accepts `source_channel: stable` for a stable
source Release; its default remains `preview`. The resolver, attestation
verifier, and `verify-source-package-metadata.py` all receive the same explicit
channel. Their standalone commands likewise accept `--channel stable` and
reject a source from the other channel. Metadata evidence binds the numeric
Release ID, tag, source commit and exact DEB/RPM bytes; packages are never
installed or executed by this preflight. This is source verification only:
stable signing, storage, installation bootstrap and publication remain disabled.

The credential-free TEST ONLY PR drill also exercises real native metadata
tools in the existing digest-pinned, network-disabled toolchain. It builds tiny
data-only stable/preview fixtures, rejects mismatched versions and RPM epochs,
and rejects a locally generated TEST ONLY RSA-signed RPM. Its synthetic source
identity is not GitHub provenance or a successful stable publication.

See [docs/release-contract.md](docs/release-contract.md) for the release,
retention, signing, and recovery contracts.

Reviewed public certificates are published at the stable URLs
`https://packages.githubim.com/keys/apt-preview.asc` and
`https://packages.githubim.com/keys/rpm-preview.asc`. The APT
`wukongim-archive-keyring` and RPM `wukongim-release` packages distribute those
certificates and dedicated repository definitions. Their indexed bytes and
friendly `/bootstrap/` downloads are identical. The stable `/repo` entrypoint
is generated from those exact paths and digests; all three are closed over by
each immutable package audit snapshot.

## Offline object-storage preparation

The [offline transport planner](docs/object-storage-planning.md) inventories
existing preview audit snapshots and explicitly retained dependency generations
without credentials or network access. Its output is a plan only; it does not
sign, upload, activate CDN routing or enable the stable channel.

The [offline delivery preparer](docs/object-storage-delivery.md) consumes either
the original preview plan or an explicit composite plan, materializes exact
objects and previews routes. Its in-memory test seam exercises upload/activation
failures without a cloud provider; all completion evidence remains simulated.

## Unpublished stable bootstrap preparation

The offline builder can prepare separate stable APT/RPM repository-setup
packages from the reviewed bootstrap version and stable public roles:

```sh
python3 scripts/build-repository-bootstrap-packages.py \
  --channel stable --control-root . --output-dir /tmp/wukongim-stable-bootstrap-new \
  > /tmp/wukongim-stable-bootstrap-preparation.json
```

The output directory must not exist. The command uses only public inputs and
builds unsigned packages; it does not sign, upload, install, or enable the
stable channel. The dedicated package names and owned files can coexist with
preview. The stable RPM configuration uses the planned mirrorlist endpoint
without a floating `baseurl` fallback. These are preparation artifacts, not
publicly installable releases: current preview composition, audit and client
validation reject their explicit unpublished preparation inventory, and `/repo`
continues to select preview. See the [bootstrap contract](docs/release-contract.md#unpublished-stable-bootstrap-preparation)
for the remaining signed-repository and public-client acceptance boundaries.

## Stable offline candidate closure

The existing planner, preparer, composer, audit and independent verifier now
accept an explicit `--channel stable --offline-candidate` mode for one initial
stable release. It binds the public controls and original source preflight
bytes, prepares both product and separate bootstrap packages, consumes outputs
from the existing family signer, and reconstructs a canonical archive after
independent native signature and repository-closure checks.

These are unsealed local candidates. The stable channel remains disabled;
production publishers, audit replay and the preview transport planner still
reject candidate schemas. No production signing keys are read automatically.
The TEST ONLY native drill uses two ephemeral key families and tiny data-only
packages; it is not a production signature, a GitHub audit seal, or public
APT/DNF acceptance. See the [candidate contract](docs/release-contract.md#stable-first-generation-offline-candidate)
for exact inputs, commands, supported operations and verification boundaries.

## Stable audit sealing and offline composite planning

Explicit stable bind/seal/readback preserves the original candidate pair and null IDs.
Sealing authenticates historical control/producer Git bytes and fresh source
provenance before uploading; normal stable endpoints are fixed to GitHub.com.
TEST ONLY loopback evidence remains separate. The explicit offline composite
planner retains both channels and all supplied generations without uploading or
activating. Stable remains disabled and no production workflow is connected.
See [the operator contract](docs/stable-audit-transport.md) for identities, inputs and verification limits.
