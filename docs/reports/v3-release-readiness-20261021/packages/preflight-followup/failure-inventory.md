# Read-only source preflight failure inventory

Baseline dafbc9dbcd88eefeaabfcafa319a6dabb37738f1. Approved seams: metadata inspector CLI (source receipt, sealed seven assets, explicit source channel) and existing read-only preflight workflow. No production enablement, signing, payload execution, publishing or changes to manifests/keys/toolchain/permissions.

1. Stable source resolves/attests but hosted workflow defaults to preview and inline metadata mapping rejects stable.
2. Stable/preview classification differs between resolver, attestation and metadata; unknown input or malformed boolean accepted.
3. Filename/version can be correct while internal DEB name/architecture/version or RPM name/architecture/version/release/signature is wrong.
4. Receipt is malformed, has duplicate/unknown keys, mismatched source identity, missing/extra asset, or stale digest.
5. Metadata query reads a symlink/hardlink or bytes replaced before/while queries run.
6. Native inspector absent, fails, returns malformed/oversized output or exceeds its deadline; partial stdout must never be a success receipt.
7. Evidence fails to bind numeric source Release ID, tag, SHA, channel and exact package digests.
8. Refactoring accidentally executes or installs a source package, exposes credentials to metadata commands, changes preview mapping, or enables publish permissions.

Tests use the CLI and subprocess tool boundary. Fixture payloads used by resolver are intentionally opaque, so these alone cannot prove native package parsing. Actual native tooling validation is separate and must not be implied when tools are unavailable.
