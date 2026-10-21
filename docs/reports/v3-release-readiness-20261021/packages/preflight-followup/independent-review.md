# Independent source preflight review

Reviewed the uncommitted diff in `/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-stable-source-classification` against `dafbc9dbcd88eefeaabfcafa319a6dabb37738f1` on 2026-10-21. This review was read-only for the repository and did not invoke cloud resources or GitHub workflows.

## Result

No remaining blocking implementation finding in the inspected scope. The identified RPM Epoch identity gap was fixed by requiring `EPOCHNUM=0`. Native drill scratch HOME and RPM `_tmppath` now point to writable private scratch storage under the read-only container filesystem.

- The workflow defaults to preview, validates the source channel enum, and forwards the same validated channel to resolution, attestations, and metadata inspection.
- Metadata inspection reuses the strict receipt and seven-asset validation boundary before and after fixed native queries, rechecks the original receipt, and binds evidence to source repository, numeric Release ID, tag, source SHA and package asset IDs/digests/sizes.
- Only native metadata queries execute in the verifier. Source package binaries and maintainer scripts are not installed or executed. Inspector subprocesses use a fresh HOME and a minimal environment.
- Stable mapping, preview tilde/hyphen mapping, architecture/name, RPM release/epoch and legacy/header signature absence are enforced.
- Stable publication remains disabled. Manifests, keys, publisher/audit/signing logic, storage and toolchain pins remain unchanged; workflow permissions and source-read secrets are not broadened.
- The real native fixture drill is isolated by the existing digest pin, non-root UID, read-only control mount/root filesystem, disabled networking and resource bounds. It uploads public metadata results only. Synthetic provenance and ephemeral TEST ONLY signing are explicitly distinguished from release qualification.
- README, release contract and Unreleased changelog reflect the extended read-only scope.

## Independently executed validation

- `python3 -B -m unittest discover -s tests -p 'test_verify_source_package_metadata.py'`: 14 tests passed in 9.614 seconds.
- `python3 -B -m unittest discover -s tests -p 'test_source_preflight_workflow.py'`: 2 tests passed.
- `git diff --check`: passed, including after final documentation/scratch-path updates.

The implementation owner separately reports 52 source tests, the full contract script, and actionlint checks passing; those broader results were not independently rerun in this review.

## Unverified boundary

The seven real Linux DEB/RPM fixture cases have not run locally because Docker is unavailable. PR CI must execute the new native drill before claiming real native-tool validation. Review of its source is not a substitute for that result. No stable source Release, stable signing or stable publication has been exercised by this review.
