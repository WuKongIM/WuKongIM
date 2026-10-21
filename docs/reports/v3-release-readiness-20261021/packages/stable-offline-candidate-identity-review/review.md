# Stable candidate identity/source partial review — generation 1

Review applies only to the three frozen sources in `identity-freeze/`, base `0cd625cbee214927a81b0a1015d825a6f53fbf28`. All three hashes matched their supplied manifest. No implementation files changed; no tests, GPG, build, signing, private-file or cloud access occurred.

## Findings

1. **R1 / P1 — preview preparation enters candidate mode accidentally.** `prepare-package-site.py:644` initializes a boolean named `candidate`. Existing loops at lines 716 and 747 overwrite it with a `Path`, which is truthy. Consequently the source-selection branch around 778, bootstrap/payload inventory schema selection around 836/914, and final candidate-plan validation around 923–924 run as candidates even when CLI mode is preview. Rename the boolean or loop variables and retain a complete preview prepare success regression. The implementation author acknowledged this and separately observed preview failures in the full Python suite; this review did not run that suite.

2. **R2 / P2 — output aliases can bypass input-root separation.** Lines 650–655 compare lexical absolute paths only. Lines 734–744 check only the immediate output parent and then resolve it, without repeating the input-root overlap checks. For example, `/outside/link_to_control/existing_subdir/new-output` is lexically outside control while its immediate parent is a real directory reached through a symlink ancestor; it resolves inside control. This can cause temporary builder output or final outputs inside an input tree. Refuse linked existing ancestors before any mkdir, and recheck all resolved output paths against both canonical input roots in both directions. Add source/control ancestor-alias failures proving no input write. The author accepted this and will apply the same guard to the eventual composer entrypoint.

## Identity boundary

The source/plan layer does not claim online provenance. Production `control_sha` currently passes only a 40-hex shape check; downstream reconstruction takes that value from the external plan. It is therefore a caller-declared origin label, not proof that control bytes came from that Git tree. The author confirmed documentation will say so; no new Git protocol is requested by this review. External plan bytes, ten public control files, source assets/evidence and checked-in producer digests are the offline equality anchors. Later sealing must independently revalidate real source/control provenance and externally bind the unchanged candidate archive; it cannot fill null IDs inside the existing archive and call it the same artifact.

The reviewed plan branch strictly separates TEST ONLY fixture inputs from production preflight inputs, keeps audit/base/package IDs null, rejects base/rotation/retirement, and requires explicit stable/offline opt-ins. Local imports resolve from the producer checkout, never the data/control directory. Actual certificate topology and signatures remain the responsibility of later public verification; this review makes no native-success claim.

## Limit

This is an early sub-scope review. Final compose, archive receipt and independent verification bodies were deliberately excluded because they are still in progress. Default production-consumer rejection and complete signed-byte closure require the final frozen implementation and real Linux TEST ONLY drill before delivery.
