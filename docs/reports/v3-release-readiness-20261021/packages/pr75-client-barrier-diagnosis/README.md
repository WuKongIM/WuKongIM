# PR75 request-barrier diagnosis and repair evidence

Original source eac9d252f13ea201b405825e6303de703af08c16; original run 39631486744 attempt 1 remains unchanged. Its 33 successes / Alma case 34 failure / 15 not reached are in original-ci-inputs. DNF legitimately selected signed B in its second invocation while this TEST ONLY case required selected A. No product defect, bad signature, missing alias or delivery-core regression is established by that failure.

The actual pinned libdnf age rule and signed protocol comparison establish the test's missing phase boundary. Original CI did not capture cache mtime/phase clocks, so the exact elapsed-time or GPG-initialization trigger of that run remains unproven. See FAILURE-INVENTORY.md for hypotheses, alternatives and scope.

## Frozen repair

Worktree: /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-native-barrier-phase
Branch: codex/packages-native-barrier-phase
Base: eac9d252f13ea201b405825e6303de703af08c16
Candidate tree (temporary index, no commit): 50e099e7adcda9e205c9862dad7e1071022d2a3c
Patch: final.patch
Frozen files and hashes: final-source-freeze.json and final-source/

Only driver, its tests, stable audit transport documentation and Changelog change. The second DNF invocation in the RPM switch case explicitly reuses A metadata; its response proof now binds exact B mirrorlist before exact-A bytes under B's planned prefix within one case serial. Normal/persistent-cache/negative/APT cases preserve their command bytes. All other 114 tracked files, including production validator, signed producers, planner, fixtures, pins and workflow, are unchanged (unchanged-source-receipt.json).

## Red and green

- Original CI: real signed B download rejected by the exact-A gate.
- route-replay.red.*: actual old RouteTable + download receipt, deterministic exit 1; marker bytes, not native packages.
- existing-proof.red.*: actual old request guard accepts six invalid switch proofs; exit 1 expected.
- shipping-tests-before-code.* and shipping-tests.red.*: all tests written before code and frozen; initial 44 tests contain 73 subtest API errors because the new seam/keyword do not yet exist. This is structural red, not claimed as a native reproduction.
- signed-client.stdout: real pinned Alma DNF, HTTPS loopback, GPG metadata/RPM checks. After a recorded real 2.1-second cache age, original parameters choose B; second-phase override chooses exact signed A via actual B mirrorlist and B-prefix HTTP 200; independent fresh-B chooses B. Data-only small RPMs and a minimal signed repository are used, not production bootstrap/product binaries or release provenance.
- shipping-tests.green.*: 44 tests pass; test bytes equal the pre-code freeze.
- full-python.*: 557 tests pass, 1 pre-existing Mac RPM skip.
- contracts.*: repository contract checks and 4 tests pass.
- validation.json records commands, elapsed time and output hashes.

The two cache-age probes and two signed-probe containers completed and exact UUID inventories were empty. The signer destroyed its TEST ONLY private-key directory before exporting only public certs, signed metadata and data-only RPMs. No production keys, production network writes, cloud resources, CI retry, commit or push was performed. Original PR75 CI is not rerun. Full exact-head Linux native 49-case validation and independent code review remain required; this archive does not claim stable activation or public release.

ARCHIVE_ALLOWLIST is the finite public file set. SHA256SUMS covers payloads plus the allowlist. archive-sha256.json adds size and CRC32 for every listed file except itself; its own SHA is reported separately to the root task. Later review evidence must be added separately rather than changing this frozen payload.
