# TEST ONLY local signing request preparation

`scripts/local-signing-exchange.py prepare-rpm` prepares one **TEST ONLY**
request for a future local RPM signer. It verifies a caller-frozen descriptor,
the exact input file inventory, and the public APT/RPM certificate policy. It
does not sign, generate or import keys, read a secret home or credential store,
contact GitHub, inspect RPM metadata, accept a response, or publish anything.
Stable publication remains disabled.

Stable custody now uses the explicitly accepted ordinary local Mac model,
with encrypted operator backups on the Mac or NAS and no stable private keys
in GitHub-hosted CI. This is a policy change, not an offline ceremony. The
request tool needs public files only; it does not provision that custody.

## Command and trust boundary

```sh
python3 -B scripts/local-signing-exchange.py prepare-rpm \
  --descriptor /absolute/path/descriptor.json \
  --expected-descriptor-sha256 <caller-frozen-64-lowercase-hex> \
  --input-root /absolute/path/public-inputs \
  --request-id <new-32-lowercase-hex> \
  --output /absolute/path/new-request.json
```

The optional `--gpg /absolute/path/to/gpg` selects a **caller-trusted** executable;
otherwise the executable is resolved once from the caller's PATH. A descriptor
cannot select an executable. Do not use a fake tool outside tests. Native calls
use only fixed public `--list-packets` and `--show-keys` commands, an empty
temporary HOME/GNUPGHOME, and a small fixed environment without credentials.
Each command has a 120-second deadline and a combined 1 MiB output budget.
The checked certificate is copied into the temporary directory before GPG
reads it. Native output and packet contents are never echoed in errors.

The external SHA-256 binds descriptor bytes. It is a caller-supplied identity
anchor, **not proof of GitHub provenance or control ancestry**. All source,
audit and toolchain identities in this TEST ONLY descriptor are assertions;
their supporting files are checked for exact bytes but not interpreted or
executed. In particular, `rpm_payload` names an unsigned *candidate*: this
command does not prove it is an RPM or lacks signatures. A later signer and
verifier must establish those facts before using it. The toolchain manifest
is bound as a file; no container is run and no workflow is dispatched.

The request is canonical JSON and uses no timestamps, so identical validated
inputs and IDs produce identical request bytes. Success writes the new request
without overwriting any existing path and prints a canonical JSON receipt
containing its SHA-256 and size. `plan_only` is true; `checks.public_key_policy`
and `checks.input_bytes` are true, while `source_provenance`, `github`,
`control_commit`, `rpm_unsigned`, `signatures`, `cloud` and `publication` stay
false. These are preparation facts, not a family signing receipt or release
audit. No durable session/replay ledger is implemented: IDs bind this request,
but do not establish global uniqueness or authorize signing.

## Descriptor v1

Encoding is the existing canonical JSON convention: sorted object keys, compact
separators, UTF-8 and one final newline. Unknown or duplicate fields fail.
The exact top-level fields are `schema`, `mode`, `session_id`, `context`,
`certificates`, and `inventory`.

- `schema`: `wukongim/local-signing-descriptor/v1`.
- `mode`: only `test_only`; production/stable execution is not implemented.
- `session_id`: caller-supplied 32 lowercase hex characters. `request_id` must
  have the same form and differ from it.
- `context`: exact `source`, `control_sha`, `toolchain`, `audit` fields.
  `source` has exact `repository` (`WuKongIM/WuKongIM`), positive integer
  `release_id`, stable `version` (`MAJOR.MINOR.PATCH`), matching `tag`
  (`v` + version), and lowercase 40-hex `source_sha`.
  `control_sha` is lowercase 40-hex. `toolchain` has exact `image`
  (`ghcr.io/wukongim/native-package-signing-toolchain`), `digest`
  (`sha256:` + 64 lowercase hex), and lowercase 40-hex `workflow_sha`.
  `audit` is exactly `kind: "test_only"`, a synthetic `id` beginning with
  `test-only-`, and `base_id: null`; real GitHub audit IDs are unsupported.
- `certificates`: exactly `apt` and `rpm`. Each has `path`, `sha256`, `size`,
  `primary_fingerprint`, `current_signing_subkey_fingerprint`,
  `next_signing_subkey_fingerprint`, and
  `historical_signing_subkey_fingerprints`. Full fingerprints are uppercase
  40-hex; next is required and historical is a sorted unique array.
  The existing validator enforces certify-only primary, sign-only subkeys,
  current validity at execution time (at least 30 days), 45-day successor
  runway, and at most 180-day subkey lifetimes. RPM's whole topology must use
  RSA 3072/4096; APT retains its existing algorithm policy. All full/16/8-hex
  IDs must be distinct across both families and the checked-in preview control.
  Any secret packet or `sec`/`ssb` record fails before policy validation.
- `inventory`: unique objects sorted by `path`, each with exactly `role`,
  `path`, `sha256`, `size`. Roles are exactly one each of `rpm_payload`,
  `deb_payload`, `source_resolution`, `source_attestation`, `source_metadata`,
  `toolchain_manifest`; exactly eight `attestation_evidence` files; and 1–32
  `control` files. These role counts freeze the current seven-asset source
  preflight plus its evidence summary, without treating the files as verified
  receipts. No base, previous response, bootstrap or metadata-signing input is
  accepted. Payload suffixes must be `.rpm`/`.deb` respectively.

Every artifact uses a safe canonical relative POSIX path, lowercase 64-hex
SHA-256 and positive integer size (booleans are rejected). The two certificate
paths and inventory must partition **all files** under `input-root`; unexpected
files are rejected. The descriptor is external to that root. Root is resolved
once, then member traversal rejects symlinks at every level and all hardlinked
or nonregular files. Keep the caller-owned input directory unchanged while the
tool runs. Inputs are bounded and hashed without executing any payload.

Limits: descriptor and nonpayload evidence/control files 8 MiB each; public
certificate 1 MiB each; each payload 800 MiB; total inputs 800 MiB; at most
1,024 tree members. Actual sizes must match declared sizes before content is
read or native commands run. Output must be a new file outside the input tree
and distinct from the descriptor; existing files/directories are preserved.

## Request v1 and limits of this slice

The exact request fields are `schema` (`wukongim/local-signing-request/v1`),
`mode` (`test_only`), `session_id`, `request_id`, `descriptor_sha256`, `family`
(`rpm`), `phase` (`rpm_payloads`), `parent_verified_receipt_sha256` (`null`),
`inputs`, and `expected_outputs`. `inputs` contains every checked artifact,
including the two public certificates, sorted by path. The descriptor digest
binds the source/control/audit/toolchain and key identities. `expected_outputs`
is exactly `[{"path":"signed/wukongim.rpm","role":"signed_rpm"}]`; no future
signed bytes or signature digest is fabricated.

APT Release signing, RPM signing/content-identity comparison, signed-response
verification, metadata continuation, replay state, stable publisher integration
and public APT/RPM client checks are not available in this command. They need
their own implementation and regression evidence. Changing custody does not
relax the existing signature, independent-key, source or publication contracts.

## Lightweight regression

```sh
python3 -B -m unittest discover -s tests -p 'test_local_signing_exchange.py'
```

These CLI tests use synthetic public files and a recording **external fake GPG**
to exercise schema, topology-policy, safe-path, bounded-runner and output rules
without generating keys. They do not prove GnuPG cryptography or a real signing
ceremony. Existing native signing-material tests retain responsibility for the
shared public validator; a real public-only preflight must use real GPG and
reviewed independent certificates. Never commit TEST ONLY keys or secret files.
