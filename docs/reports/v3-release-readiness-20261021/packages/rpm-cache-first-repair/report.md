# TEST ONLY DNF cache persistence repair

Implementation and relevant/full local checks are complete. No commit, push, CI rerun, production key access, publication or cloud resource change was performed. Source is frozen at the four paths in source-freeze.json, based on 97dd5976659f1b1f86dd9180ae7fe39b2392ff44.

## Cause and repair

Generation 4's first failed case was Rocky cache-A-first (24/49 executed, 23 passed). Its native client exited 0 with valid signature and exact metadata/product responses, but the cache helper saw only 19 bytes of expired_repos.json. Exact pinned Rocky and Alma DNF configuration/lifecycle probes independently established that the early RepoPersistor retains an installroot directory while final CLI repository metadata uses /tmp/dnf-cache, which was not host-bound. See the separately frozen rpm-cache-first-diagnosis evidence; this repair does not rewrite that evidence.

The driver retains both early installroot binds and adds distinct dnf-cli-cache and dnf-cli-state host children for the actual /tmp/dnf-cache and /tmp/dnf-state locations. A small pure cache_client_command function contains the existing bind/tmpfs composition and is used by the real client call. It preserves the entire native body/options, changes no DNF flags and replaces only a tmpfs exactly at a bind destination. Native cache paths are not collapsed onto one host directory.

Only metadata under the original dnf-cache or new dnf-cli-cache can count. State files, downloaded RPMs and arbitrary logs cannot pass. Same inode/device/content/size plus atime advancement after epoch arming remains mandatory for reuse. Bounded inventory, privileged-helper isolation, request/source verification and owned cleanup are unchanged; unchanged-boundaries.json records exact source block hashes. Production validator, image pins, planner, workflow, archive/signing producers and the 49 native case count remain unchanged.

## Tests-first evidence

All shipping tests were written before implementation and frozen in before-implementation/. The final test file is byte-identical to that freeze. 01-old-source-red.log records 31 test methods on old source: 2 assertion failures and 6 errors, covering missing actual-path mounts, unrecognized new-root metadata, and the as-yet absent pure assembly function. Missing-function errors are API-structure red evidence, not a claimed native reproduction. The original real native failure is preserved separately under pr74-ci-generation-4.

Regression coverage includes exact four distinct mounts, unchanged APT mounts, unsupported family, actual production command composition and unchanged native body, exact tmpfs replacement, preserved neighboring tmpfs, nonmutating/pure assembly, accepted metadata forms under both roots, rejected expired/state/package/log/partial-list inputs, real OS reads after epoch arming, rejected mere observation and changed device/inode/hash/size/atime/removed files, new-root symlink/hardlink/oversize/empty-file guards, and removal of all owned cache children with an external sentinel preserved.

- 02-related-green.log: 65 driver and production-validator tests pass.
- 03-full-suite.log: 518 tests pass, with 1 existing native RPM availability skip on this Mac. Printed false/failed fixture JSON at the end is expected negative-test output; the unittest verdict is OK.
- 04-contracts.log: publication-control validation, 4 contract tests and package repository contracts pass.
- git diff --check passes; only the 4 owned files changed.

The next exact-head native run must prove actual Rocky/Alma cache-A-first, cache-A-repeat, cache-B-target and all remaining cases. Configuration probes and local filesystem tests do not establish signed native cache reuse. The strict read-proof gate has no file-presence fallback and does not demand zero network requests.
