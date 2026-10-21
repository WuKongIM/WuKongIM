# Public signing keys

Only reviewed public certificates and binary public keyrings belong here.
Secret keys, secret subkeys, passphrases, revocation certificates, temporary
GnuPG homes, and test keys must never be committed or uploaded as artifacts.

Publication remains disabled until a reviewed signing manifest names exact
40-hex primary, current, and next signing-subkey fingerprints for both the APT
and RPM preview certificates. Historical fingerprints, when present, must be
sorted and must name public-only former current sign-only subkeys contained in
the same certificate. They may still be valid, expired, or revoked, but cannot
expire after the current subkey and must never be disabled, invalid, or
future-created. Expired or revoked historical keys remain audit topology only;
a live RPM payload must be retired before its actual signing subkey ceases to
be usable. Only a currently usable historical key may authorize a preserved
RPM payload.

The fingerprints and public certificates are committed review inputs. GitHub
variables must not override them. Every APT and RPM primary, current, next, and
historical fingerprint must be distinct so that one credential cannot silently
cross a trust boundary. Their trailing 16-hex and 8-hex key IDs must also be
globally distinct because RPM tooling may report only a shortened key ID.

The RPM certificate has an additional non-negotiable algorithm contract: its
primary plus every current, next, and historical signing subkey must be RSA
(GnuPG colon public-key algorithm `1`) with exactly 3072 or 4096 bits. RSA2048,
ECC, and every other algorithm or key size are rejected both by reviewed
control validation and by the isolated signer before and after secret-subkey
import. APT keys do not inherit this RPM-specific RSA restriction. RPM package
and repository-metadata signatures use SHA-256 (OpenPGP hash algorithm `8`).

While `manifests/preview-signing.json` has `enabled: false`, its primary,
current, and next fingerprints remain `null`, `historical` remains an empty
array, and the public certificate files may be absent. Enabling signing
requires adding the reviewed `apt-preview.asc` and `rpm-preview.asc` files and
their exact uppercase fingerprints in the same protected change. Each first
production certificate must already include its next subkey. Private primary
keys, private subkeys, passphrases, revocation certificates, and test keys
remain forbidden here.

Preview CI current signing-subkey exports and their passphrases may exist only
as the fixed secrets in their corresponding protected signing Environment.
They must not be copied to repository-level or organization-level secrets, or
into the other package-family Environment. This restriction on CI exports does
not move operator-retained primary/next keys or backups into GitHub. Stable
private keys use the accepted local Mac custody model, with encrypted backups
on the Mac or NAS, and never enter GitHub-hosted CI; see the
[release contract](../docs/release-contract.md). This is an online-workstation
model, not an offline-signing claim.

`manifests/stable-public-keys.json` is the sole public role declaration for
`apt-stable.asc` and `rpm-stable.asc`. It records only the certificate path and
primary/current/next/historical fingerprints; it has no secret, Environment or
publication-enable fields. The same public certificate checks and 30/45/180-day
policy apply, and full fingerprints plus 16-hex and 8-hex key IDs must be unique
across both channels and both package families. Historical control roots may
omit this entire registration; a partial registration is rejected.

This registration does not enable stable signing or publication. Preview
snapshots, audit archives and bootstrap packages continue to contain only
their reviewed preview certificates. Stable private material remains on the
operator's Mac; the public role declaration is not backup-recovery evidence.

For local signing, `scripts/sign-package-family.py` accepts exactly one of
`--secret-subkey-base64-file`, `--secret-subkey-base64-stdin`, or the existing
`--secret-subkey-base64-env`, and exactly one of `--passphrase-file` or
`--passphrase-env`. Files pass the same validator as CI inputs: secret files
must be ordinary single-link files with no group/other permissions (use mode
0600), and the base64 input must contain only the encrypted current sign-only
subkey export. A full primary/next backup is not a signing input. Keep these
files outside the repository, public output tree, and artifact directories;
the tool does not retrieve credentials, generate keys, or publish its output.
The existing public-certificate, exact signer, SHA-256, and atomic-output checks
apply to every input method.

The publisher copies these exact certificate bytes to the stable public paths
`/keys/apt-preview.asc` and `/keys/rpm-preview.asc` and records their SHA-256,
size, and topology in the snapshot and audit receipt. The reviewed bootstrap
builder also embeds the complete APT certificate in
`wukongim-archive-keyring` and the complete RPM certificate in
`wukongim-release`. Both packages are indexed in their signed repositories, so
an installed client can receive a later certificate before a newly added
successor subkey is promoted. The first direct bootstrap-package download
still trusts the HTTPS origin; automatic updates begin only after that package
has installed its dedicated key and repository definition.
