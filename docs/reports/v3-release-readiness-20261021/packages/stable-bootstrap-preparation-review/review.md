# Stable bootstrap preparation — independent review

Reviewed 2026-10-21T08:47:37.212838+00:00 against packages base `51bfedbe88c78758fa05bfb87b13541b4bca4796`, branch `codex/stable-bootstrap-preparation`, working-copy patch SHA-256 `ff0e17a5bf5866ea4cc2d5d818f790838772c3a9f7771e359a22d10c47fad392`. Exact nine-file and 34 protected-file hashes are in `review.json`. This is a read-only review; the reviewer did not execute tests, Docker, GPG, signing or package installation, and accessed no private directory, Keychain or NAS.

## Standards

No blocking findings in this slice. Applicable root AGENTS and package contracts were frozen in `review-scope.json`; no package AGENTS/FLOW files are tracked. Test-first failures and subsequent results are preserved in the implementation evidence rather than inferred from source: builder initial red/green, separate public-certificate replacement red/green, native harness and workflow contract red/green, then 52 related tests (one existing native RPM skip) and contract pass. These are author-run receipts, not independent repeated tests.

The change uses a closed two-channel layout through the existing builder, preserving its exclusive output-directory rename and failed-stage cleanup. Stable rejects explicit manifest/certificate overrides and derives fixed public paths from `--control-root`; executable validation imports from the builder checkout, not that data root (`scripts/build-repository-bootstrap-packages.py:656`). Public topology/lifetime and cross-channel fingerprint checks reuse the unchanged validator. Captured stable public bytes are compared again after validation before they become package contents. This rejects the recorded post-validation certificate replacement; it does not assert resistance to an adversary repeatedly rewriting a caller-supplied checkout or independently establish GitHub provenance for an arbitrary root.

## Spec

No blocking findings. Preview constants (13) and unchanged serialization/inspection/output helpers (15) were independently AST-compared with the base. New arguments default to preview; existing preview inventory fields and layout remain the same. Full native preview RPM byte compatibility remains dependent on the planned CI run; static comparison and fake-RPM local tests are not presented as native execution.

Stable bootstrap packages have independent package names and owned files. APT uses stable Suite and a dedicated keyring; RPM keeps gpgcheck/repo_gpgcheck/TLS enabled and uses the fixed planned mirrorlist with no baseurl fallback. `wukongim.stable_bootstrap_preparation/v1`, `channel: stable`, `published: false` prevent the existing preview composer from accepting this inventory. The disabled channel, preview manifests/keys, production workflows, signer, audit/archive/composer and public-client code are among the 34 byte-identical protected files.

The six-case native harness genuinely invokes the real builder, `dpkg --root`, `rpm --root`, native package databases and `rpm --verify --all` (`tests/native_bootstrap_preparation_drill.py:125`). It installs both orders and reinstalls each package, checking exact identities, retained bytes, 0644/root ownership, independently dearmored APT keyring and public RPM certificate. No install command uses signature, dependency or file-conflict bypass flags. The existing unsigned builder's digest-only RPM inspection is unchanged; this drill creates unsigned bootstrap packages and does not prove package signatures, APT/DNF repository trust or public installation. Documentation states those limits explicitly.

The added container is digest-pinned, network-disabled and read-only, with all capabilities dropped except SYS_CHROOT, no-new-privileges, bounded resources, read-only control and only a JSON evidence output mount. Root/SYS_CHROOT are scoped to native scratch-root package installation; no production key or host Docker socket is mounted. Static review supports this isolation rationale but cannot certify that these capabilities suffice on the actual image until CI executes. Public input/receipt identity comes from the fixed PR checkout and workflow's GITHUB_SHA; the CLI source_sha field alone is a caller declaration, not independent provenance.

## Pending validation

The six new native cases have **not run** because the local Docker daemon is unavailable. Proceed to the first automatic PR CI, retain any failure, and diagnose its exact output. Do not mark native coinstallation, mirrorlist behavior, signed stable package delivery or A/B CDN transitions passed from this review. Stable remains unpublished. No new release gate or approval process is introduced by this review.

Standards: 0 blocking findings. Spec: 0 blocking findings. Native execution is pending, not passed.
