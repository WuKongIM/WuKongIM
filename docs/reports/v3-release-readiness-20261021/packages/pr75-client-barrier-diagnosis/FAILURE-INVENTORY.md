# PR75 native request-barrier diagnosis (TEST ONLY)

Source eac9d252f13ea201b405825e6303de703af08c16; original attempt 1 retained separately. 33 cases passed, Alma case 34 failed after native DNF succeeded with signed B 3.0.1; 15 cases did not run. Cleanup was verified empty. PR75's five delivery files do not change the driver, validator, workflow or fixtures.

## Ranked hypotheses and evidence

1. Two separate DNF invocations can change metadata selection after the first A mirrorlist response has switched the server to B. Prediction: expired A metadata makes the second invocation load B; explicit second-stage reuse of authenticated A metadata preserves exact A while package resolution still sees the B mirrorlist. Confirmed by the pinned native signed probe.
2. Concurrent fixture state could cross case/generation boundaries. Prediction: the same-case route sequence would be inconsistent with serial/mode. Not supported: serial 35 consistently implements the documented A-first/B-after route, and actual RouteTable replay reproduces the valid B request sequence.
3. Alias/planner or product bytes could mix generations. Prediction: B URL returns A/wrong bytes or a missing payload. Not supported: original B bytes match baseline B, all responses were 200, signatures passed, and the exact-A host gate correctly rejects B. This is a test transaction-selection defect, not established product or offline-delivery failure.

Actual libdnf 0.69 in both pinned Rocky/Alma images evaluates age using integer seconds and expires only when age > metadata_expire. Thus metadata_expire=0 does not force expiry in the same second; it permits expiry later. Original CI lacks the primary mtime and phase clock, so the exact contribution of elapsed time versus GPG initialization is not retrospectively established. Duplicate repomd/asc requests are consistent with the library's GPG import/retry and repomd revalidation paths, but that is not a measured timing attribution.

## Bounded proof

`route-replay.py --assert-exact-A` exits 1 at the actual host `_download_receipt` gate using the actual RouteTable and captured URI sequence. Marker bytes are not a native RPM test.

`run-signed-probe-owned.py build` exports only public signed data-only RPMs and metadata after destroying the TEST ONLY private-key home. `run-signed-probe-owned.py client` uses pinned Alma, network=none, real HTTPS on container loopback, the actual RouteTable, repo_gpgcheck=1, gpgcheck=1 and sslverify=1. A real 2.1-second wait after A makecache records unchanged mtime/hash and later time. Original parameters choose signed B; second-stage repo metadata_expire=-1 chooses signed A through B mirrorlist + B prefix HTTP 200; independent fresh-B with original parameters chooses B. This proves the CLI/protocol mechanism, not production bootstrap/product acceptance. Both owned container inventories are empty.

## Scope and alternatives

Only the TEST ONLY RPM request-barrier second download invocation should explicitly reuse selected metadata. Keep makecache --refresh, packaged metadata_expire=0, production validator, signatures, all other cases, exact source/version/hash checks and 49-case inventory unchanged. This is an explicit phase contract, not a finite freshness delay. Do not freeze server responses or accept arbitrary A/B results.

Global --cacheonly prevents uncached payload downloads. Pinning A NEVRA cannot resolve A from B-only metadata. Repo-only ONLY_CACHE is possible but requires a broader Python DNF transaction entry seam. The one CLI repo override is narrower and has actual B-prefix proof.

## All shipping failure modes (written before implementation)

- Transformation changes the first makecache, bootstrap/signature/CA checks, other args or original command object.
- The second RPM switch stage remains age-dependent, or the override leaks to fresh B, normal/persistent-cache, negative cases or APT.
- Upstream command drift removes, duplicates or reorders makecache/install and silently bypasses the stage contract.
- Switch proof accepts only A mirror/A payload; accepts an arbitrary retained C generation instead of explicit after-generation B; or accepts B payload before B mirrorlist.
- Request proof mixes case serials, is missing a serial, accepts incomplete/failed B mirror or payload, or accepts wrong hash/size, source/member, target version or metadata generation.
- Native signature, exact installed source/version or cache-read checks are weakened. Existing rejection suites remain required; no new acceptance alternatives.

Tests for the new command seam and strengthened request proof are written together before code. The prior native failure and signed original-behavior red remain distinct from structural API tests that initially fail because the new seam is absent. Final exact-head full native 49 remains a later Linux CI requirement.

## Primary sources

- https://raw.githubusercontent.com/rpm-software-management/libdnf/0.69.0/libdnf/repo/Repo.cpp (age, resetMetadataExpired, load/loadCache, isInSync, GPG retry)
- https://raw.githubusercontent.com/rpm-software-management/dnf/4.14.0/dnf/base.py (_select_remote_pkgs global cacheonly rule)
- https://dnf.readthedocs.io/en/latest/conf_ref.html (metadata_expire and repository options)

Bounded source copies and hashes are in upstream-source-receipts.json. Native cache-age probes verify the relevant behavior in the actual pinned distributions, not just upstream source.
