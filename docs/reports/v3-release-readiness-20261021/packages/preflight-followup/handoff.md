# Stable source preflight follow-up — 2026-10-21

Baseline packages `dafbc9dbcd88eefeaabfcafa319a6dabb37738f1`, existing PR 67 worktree. No commit, push or dispatch by this agent.

## Result and boundary

Completed the independent read-only chain: default-preview `source_channel` is validated once and passed explicitly through resolver → fixed-workflow attestation verification → native metadata inspection. The new metadata-only CLI reuses exact source-receipt validation, verifies the complete seven-asset byte closure before/after subprocess queries, and emits v2 evidence binding channel, numeric Release ID, tag, source SHA and both package asset IDs/SHA-256/sizes.

Stable versions map to X.Y.Z; existing preview ~ / hyphen mapping is preserved. DEB/RPM names and architectures, RPM release 1/epoch 0, and absence of all four legacy/header signature fields are enforced. Native query processes receive only tool PATH, C locale and a new scratch HOME. No payload is installed/executed. Metadata evidence alone does not prove provenance: the hosted workflow requires the preceding attestation step.

Stable manifests remain disabled and empty. No manifest, certificate, secret, signing/publisher/audit/storage/bootstrap code, toolchain pin or permission changes. Package control must still undergo parent review/PR CI before merging. No current stable source release has been checked by this task.

## Tests-first evidence

First red and all subsequent red/green logs are retained here. The missing CLI initially failed 14 tests; a later independent review found the omitted RPM Epoch identity, captured as one red failure before adding EPOCHNUM=0. Workflow channel/native-drill contracts were also red before YAML changes.

Final local result: 52 source-related tests PASS (11.763s), tests/contract.sh PASS, actionlint 1.7.9 both changed workflows PASS, four Python files AST parse PASS, git diff --check PASS. Independent reviewer re-ran 14 metadata + 2 workflow tests and found no remaining blocking issue. Exact commands and file digests: validation-summary.json.

## Real Linux acceptance still pending

This macOS host has no dpkg-deb/rpm and Docker daemon is unavailable. Local subprocess-boundary tests are not real native parsing evidence. The existing credential-free TEST ONLY PR drill now adds seven real native cases using the existing reviewed signing-toolchain image digest. The non-root container has no network, a read-only control mount, dropped capabilities, private bounded tmpfs, CPU/memory/PID limits and a five-minute deadline; exact-owned container cleanup is trapped. No toolchain pin upgrade.

It builds tiny data-only stable/preview packages and tests wrong channel, wrong DEB version, wrong RPM version/epoch, plus a real RSAHEADER signed-RPM rejection. The one temporary unprotected TEST ONLY RSA key stays in scratch and is destroyed, with no production key access. Native evidence records actual signature tags observed. All four signature fields have independent CLI stub rejection cases; native drill does not claim historical SIGPGP/SIGGPG/DSAHEADER signing formats were generated.

Artifact after PR CI: native-source-metadata-test-only/native-source-metadata.json; seven passed cases and passed=true are mandatory. Failure receipts are uploaded when available; absence of a receipt never satisfies the step. Scratch data/keys are never uploaded. Existing full TEST ONLY repository drill still runs afterward.

## Parent handoff

Review ten changed/new files listed in validation-summary.json and independent-review.md, commit/update existing PR 67, let its CI exercise the real Linux cases, inspect the actual artifact and retain any first failure. Only then report this narrow source-preflight implementation validated. This work does not unblock stable signer/storage/publication or replace exact-version public APT/RPM client acceptance.
