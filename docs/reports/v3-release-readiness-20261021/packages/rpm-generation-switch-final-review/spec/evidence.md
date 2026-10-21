# Spec review evidence

Scope: `git diff HEAD` at `f2c37e92b09bcd11599d31449bbca5de0ac52c25`; no commits, no untracked files. Six working-copy files were frozen and rechecked unchanged in `source-freeze.json`; source copies and full patch are retained alongside it. Independent read-only review; no implementation edits or tests were executed.

## Requirement mapping

| Approved requirement | Reviewed implementation / evidence | Assessment |
|---|---|---|
| proposal:33 “After each supplied stable snapshot has completed P.verify_candidate and bounded archive verification” | planner:299–395 resolves exact candidate/archive anchors, invokes real `P.verify_candidate` at343 before bounded extraction/catalog at347–364; original members are independently hashed, archive and candidate anchors rechecked before return. `verify-stable-seal-inputs.py:302–319` delegates actual audit/signature verifier. | Implemented. Structural fixtures explicitly mock crypto; no claim that those mocks prove signing. |
| proposal:33 exact product+bootstrap canonical RPM; inventory:11 shape/path bounds | planner:355–364 compares the canonical Packages set with exactly the product path and validated bootstrap path. Existing composer `validate_rpm_tree:1709–1824` closes the signer tree over exact payload/metadata members. | Implemented. No arbitrary archive cross-product. |
| proposal:35 collision checks include snapshot_objects and aliases | planner:450–467 indexes native routes and rejects SHA/size/cache disagreement before alias insertion; identical native routes omit overlay aliases. tests/composite:326 onwards injects native-route SHA/size/cache conflicts. | Implemented. Original objects retain native storage keys. |
| proposal:35 deterministic same-byte source with actual original member | planner:468–478 sorts stable audit IDs, selects first identical canonical payload, rejects conflicting same-name signed bytes;489–498 enumerates aliases preserving original source member. tests/composite:251–315 checks actual archive extraction and retained-order canonical equality. | Implemented. Destination B never invents a B archive member for A bytes. |
| proposal:37 ≤7 stable /84 absent cross aliases, no preview or metadata crossing | planner:403–426 keeps max8 total and required current preview+stable; each stable has two payloads. At most2*S*S routes minus2*S originals =>2*S*(S−1)≤84. tests/composite:373–390 checks84 and early max8/2GiB rejection. | Implemented using existing bounds. |
| proposal:39 exact A metadata; inventory:15–17 validator/payload proof | driver:157–172 enforces exact selected A metadata URL, successful complete200 SHA/size or304 empty body, expected ETag and matching request validator; If-None-Match wins over If-Modified-Since. driver:173–194 requires supplied stable prefix, canonical selected member, plan mapping, original-source equality, immutable cache identity and complete200 A bytes. | Implemented. Unknown source/member, other family/architecture/version, incomplete response and mismatches reject. |
| inventory:14/18/19 actual clients, current target and negatives unchanged | Only helper invocation/receipt expansion changed in driver:850–868. Shared `validate-production-package-clients.py:330–332` still invokes separate `makecache --refresh` and `install --downloadonly`; unchanged helper:1432–1470 keeps genuine mirrorlist bootstrap. Case registration, original negatives, owned cleanup, fixed source and producer/workflow/channel/key files are outside the six-file diff. | Implemented; expected49 native cases remain. |
| proposal:51 next exact-head native evidence | Original gen3 summary records22 passed /23 executed of49, then Rocky switch failure and26 unreached. Driver/route tests cannot establish the unreached native cache cases or repaired49/49. | Validation boundary; next native CI is still required, not a static implementation finding. |

## Persisted-cache assertion audit

The new verifier requires both some selected metadata traffic and a payload response (driver:197); all mirrorlist responses must be complete200 bytes of supplied stable generations (145–152). This is stronger than the old aggregate path check, but the actual harness has relevant constraints:

- Exact bootstrap repository configuration sets `metadata_expire=0`; shared validator:325–332 retains `cachedir`/`persistdir`, `keepcache=False`, forced `makecache --refresh`, then `install --downloadonly --downloaddir=/downloads`.
- Driver:825 creates a new per-case download directory; driver:766–781 reuses only the separately mounted DNF cache/state. It does not reuse the output payload directory. `cache_evidence` still requires actual existing metadata reads for cache-A-repeat.
- Metadata200 or object-bound304 is supported. No code change forces signature checking or cache-read evidence off.

Upstream primary-code cross-check supports this static interpretation: [DNF4 repository payload target](https://raw.githubusercontent.com/rpm-software-management/dnf/4.14.0/dnf/repo.py) writes to `pkg.pkgdir`; [libdnf0.69 Repo.cpp](https://raw.githubusercontent.com/rpm-software-management/libdnf/0.69.0/libdnf/repo/Repo.cpp) refreshes expired cached metadata through `isRepomdInSync` before reusing it; [librepo1.14.5 handle.c](https://raw.githubusercontent.com/rpm-software-management/librepo/1.14.5/librepo/handle.c) downloads a remote mirrorlist into a new temporary descriptor for parsing. These reference versions are corroborating source evidence, **not proof of the exact digest-pinned container builds**. The original gen3 run did not reach RPM cache-A-repeat/B-target, so retain that execution limitation until exact-head native CI. No proven legitimate-cache rejection was found; no speculative new blocker is introduced.

## Findings

- Missing/partial implementation requirements: 0.
- Scope creep: 0.
- Incorrect implementation: 0.
- Remaining validation: next exact-head native49-case run, explicitly sequenced after this review by proposal:51. No claims of public repository verification or stable release readiness.
