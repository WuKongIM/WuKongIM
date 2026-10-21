# Stable source classification: failure inventory before implementation

Frozen base: `WuKongIM/packages@feb7f21503d35133cf23df7d7ab250c5d4129e71`.
Authorized seams: resolver CLI, attestation verifier CLI, existing control validator.
No production workflow, manifest, signer, audit identity or deployment change.

1. A well-formed immutable stable source cannot currently be resolved even for read-only inspection.
2. Allowing stable globally could silently widen current preview automation. Default channel must remain preview.
3. Explicit stable inspection must reject a prerelease tag, and preview inspection must reject a stable tag.
4. Tag classification and GitHub/receipt `prerelease` must agree exactly; booleans only, not truthy strings or integers.
5. Leading-zero versions/prerelease identifiers, missing `v`, build metadata and unknown channels must fail closed.
6. Stable receipt verification must retain the numeric ID, exact seven-asset checksum closure, main ancestry/revalidation flags, fixed source tag/SHA and signer-workflow restrictions.
7. Stable attestation verification must reject a missing explicit stable channel, incorrect receipt identity or failed external provenance check without producing accepted evidence.
8. Read-only stable inspection must not enable publication: unchanged production control must still reject stable activation or a stable version inserted into preview.

Use existing tiny opaque assets and loopback GitHub fixture, with a fake `gh` only at the external attestation boundary. Exercise the two CLIs as real processes. No package build, Docker, provider calls or signing credentials.
Retain red/green test logs and final diff checks under this evidence directory.
