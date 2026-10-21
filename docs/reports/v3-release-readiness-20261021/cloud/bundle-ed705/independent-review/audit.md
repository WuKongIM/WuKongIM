# Offline Cloud Deployment Bundle: independent static review

Source and control: `ed705c0fbffdea485bbb65608d6e340bb049d97e`.
Request: `v3-readiness-ed705-20261021`.
This review read committed source and performed small local hashing only. It did not build or execute bundle code, run tests/load, query GitHub/cloud, read credentials, dispatch workflows, activate hosts, or alter the repository. Actual run/artifact acceptance remains the caller's separate evidence step.

## Authority decision

The reviewed workflow contains one `build` job, restricted to `refs/heads/main`, on Ubuntu 24.04 with a 45-minute job timeout. Top-level token permission is only `contents: read`; there is no job override, Environment, secret reference, OIDC permission, cloud role, Lease identity, SSH identity, procurement command, activation invocation, reusable-workflow dispatch, or release/publication destination. Checkout credentials are not persisted. Every external Action is pinned by a full commit SHA. Artifact upload is intentional and uses GitHub Actions' runtime artifact facility; “credential-free” means no provider/runtime/standing secret, not absence of GitHub's own job/action tokens or all network activity.

The workflow builds assets and product/control binaries, downloads three checksum-pinned dependencies, seals, verifies, archives, and uploads an artifact. No bundled script or service is activated. The only binaries directly executed after construction are the trusted-control `wkcloudbundle seal-offline` and `verify-offline` commands. Their offline command bodies call the root-anchored directory adapter and pure bundle use case. This is sufficient static authority evidence for this one pre-procurement job; it is not live deployment, long-run, Linux performance, native-package signing, or v3 GA evidence.

Evidence: `.github/workflows/cloud-deployment-bundle.yml:16-17,23-46,60-156`; `cmd/wkcloudbundle/offline_commands.go:13-58`; `.github/workflows/README.md:951-961`. Exact bytes of all reviewed governing rules, FLOW documents and contract sources are frozen in `source-freeze.json`.

## Evidence the caller must retain

1. Authenticated GitHub run metadata: exact repository and head repository `WuKongIM/WuKongIM`, workflow `.github/workflows/cloud-deployment-bundle.yml` (optionally `@refs/heads/main` suffix), `workflow_dispatch`, head branch `main`, exact head/control SHA above, run ID, attempt, conclusion and timestamps. Preserve the exact dispatch inputs/request correlation and no-reuse search separately. A matching title alone is not provenance. Prefer this authorized first attempt; record any retry rather than silently mixing runs.
2. Successful identity, asset build, Linux binary build, dependency checksum, seal/verify, and artifact-upload job steps for that exact run. Identity logs/commands must prove source checkout is the requested SHA and is reachable from main, while control checkout equals the run SHA. Because source and control are separately selected, both must match the intended SHA here.
3. Exact artifact metadata: artifact ID, name, run association, immutable ZIP SHA-256, size and expiration. Require the run's single selected `cloud-deployment-bundle-<bundle digest hex>` artifact and preserve its download metadata. Seven-day Actions retention makes durable small receipts useful.
4. Independently computed downloaded ZIP digest and size equal that metadata. ZIP must contain exactly `cloud-deployment-bundle.tar.gz`, `cloud-deployment-bundle.tar.gz.sha256`, and `bundle-manifest-output.json`, with no duplicates, aliases or unexpected entries.
5. Compute the tar.gz byte digest from its stream. The checksum sidecar must be exactly one supported SHA-256/filename record referring to that tar.gz. Archive SHA is distinct from bundle digest.
6. Inspect and hash tar members without extracting or executing them. Bind each regular member's normalized path, exact permission mode, byte size and SHA-256 to the embedded manifest, and compare the embedded manifest to `bundle-manifest-output.json`. Require exact source/control, artifact name and ordered bundle digest agreement.
7. Keep the independent verifier's own bytes/digest, result JSON, manifest bytes, canonical intent bytes, and this audit. The success receipt must state which checks ran and which semantics still depend on trusted runner seal/verify. Do not claim independent cryptographic source provenance from a self-declared manifest.

## Independent algorithm

`bundle-manifest.json` has schema `wukongim.cloud_deployment.bundle/v1`. Its file inventory is every regular file except itself, ordered by Go string order on slash-separated relative paths (UTF-8 byte order). Directory records are excluded. The inclusive regular-file count is at most 2048. Each inventory record is `{path, mode, size, sha256}`; modes and sizes are JSON integers, and content SHA-256 is lowercase hex without a prefix. File count, order and all four fields must match exactly; reject unexpected or duplicate fields/JSON keys in the independent reader.

For each ordered record append these exact bytes:

```text
UTF8(path) + NUL + ASCII(decimal permission mode) + NUL
+ ASCII(decimal size) + NUL + ASCII(lowercase content SHA-256) + LF
```

The manifest `bundle_digest` is `sha256:` plus lowercase SHA-256 of their concatenation. Decimal permission examples: `0644` = 420, `0755` = 493. Directory modes, uid/gid and timestamps are not part of this digest, while archive hashing binds the entire tar.gz representation. The manifest itself is excluded to avoid a recursive digest, but must be present and mode 0644.

`intent_sha256` is `sha256:` plus SHA-256 of the exact complete `deployment-intent.json` bytes, including terminal LF. The workflow's sealer writes Go `json.MarshalIndent(value, "", "  ")` plus one LF; it does not use JSON key sorting. Use the struct field order and DefaultIntent array order. Our independently reconstructed exact expectation is saved as `expected-deployment-intent.json`: 1627 bytes, SHA-256 `c0ba320bc1d656ea4b7429aa592b306434bc8dccb0fcc088a9f3238bcbb30c85`. All fields are ASCII, so the independent two-space Python JSON rendering matches this Go encoding for this exact value. Compare full bytes, not only selected fields or an object round-trip.

The expected intent fixes Ubuntu 24.04 / amd64, 3 service nodes + 1 load node, 256 hash slots / 12 workload Slot Groups / 3 replicas, five exact root-owned runtime-only secret file declarations with decimal mode 384, the full ordered base-tool list, and exactly nine ordered offline binaries. The byte expectation closes the schema, types, unknown/duplicate fields, values and array ordering without executing Go.

## Tar handling contract for independent verification

GNU tar emits a root `./` directory and typically prefixes all member names with `./`. Strip at most that one prefix; ignore exactly the single root directory. Permit directories and ordinary regular files only. Reject symbolic/hard links, GNU sparse and contiguous types, special files, nonempty link targets, absolute paths, backslashes, control characters, noncanonical separators, `.`/`..` components, duplicate canonical paths, and any file used as another member's ancestor. Do not use `extractall` or execute packaged binaries. Enforce finite member count and decompressed-byte limits before reading data; limits beyond the source's 2048 regular-file cap are verifier resource policy and should be recorded separately.

Check regular member byte counts against tar header sizes. Hash every member fully. Require mode 0644 for manifest/intent and each required ordinary config/template/service file, and 0755 for required scripts and nine binaries. Check each expected binary's first 20 bytes for ELF magic, 64-bit/little-endian, OSABI 0 or 3, executable/shared type 2 or 3, machine 62, and mode 0755, matching the current source validator. Header checks establish file format, not execution success. Explicitly reject GNU/PAX extension effects that create links/sparse files or path ambiguity; when using a parser that consumes extension records internally, keep this limitation clear and validate its final resolved member properties.

Read the ZIP's manifest and sidecar only after small individual size checks. A broad 1 GiB member cap is not a safe read-all bound for a JSON document or checksum sidecar. Tar payloads should stay streaming. Do not assume one self-consistent archive is a reproducible build; this run proves content identity, not cross-run reproducibility.

## Static limits and verifier review

These are evidence boundaries, not demonstrated defects in this exact artifact:

- `Verify` hashes raw intent bytes and validates the parsed DefaultIntent; it does not independently enforce canonical byte serialization. Its JSON decoder rejects unknown fields and trailing data but not duplicate JSON keys. The independent verifier should reject duplicate keys and compare the complete canonical expectation above.
- The directory adapter rejects symlinks/nonregular entries but treats filesystem hard links as regular files and does not authenticate a tar container. Therefore tar-link/sparse/path checks belong to the independent archive pass before any possible extraction.
- `verify-offline` validates required file presence/modes, ELF headers, fixed intent, workload fragments/pair equivalence, secret-shaped names and container exclusions. It does not authenticate source/control GitHub identities, scan arbitrary bytes for secrets, compare every scaffold byte to the control source, or parse the formal YAML as a full semantic model. Successful trusted build/seal provenance remains necessary. Workload files can additionally be compared byte-for-byte with this exact source revision.
- This is a networked CI build with pinned revisions/locks/checksums, not an air-gapped or hermetic build. The workflow does not test all binaries by running them or prove four-host readiness. Asset/package-manager execution is under the reviewed source trust boundary.
- A same-binary seal/verify agreement is a consistency check. The independent algorithm closes the byte/inventory seam; neither is a package signature or GA acceptance.

The caller's initial `verify-artifact.py` was read without editing it. Concrete recommendations sent: (1) compare exact independent intent bytes because selected checks accepted changed/empty secret/tool arrays and unknown fields; (2) cap ZIP manifest/sidecar sizes before read-all; (3) use explicit regular/directory tar types and reject sparse/unsafe ancestor paths. The script already streams tar payloads, verifies ZIP and archive digests, rejects duplicate JSON keys and obvious tar links, hashes all regular members, checks exact inventory/bundle digest and ELF headers, and explicitly limits its success claim to independent bytes/selected semantics.
