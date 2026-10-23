# Exact 56c native diagnostic-candidate construction

Fresh independent clone with a real `.git` directory, exact source **56c287493a5e9fc3df65ea0631b99612672cd678**, tree **582e5bc024147b32aa68074d04c041391af89b96**. Source is clean before and after. The 13-file delta from merged 27fa6d63cf3b2b6c3a9a28575f917d60cc054ded matches the reviewed candidate: bounded Metrics/finalization diagnostics, tests and documentation. No source or generated asset was changed during construction.

Exactly two fresh native Darwin/arm64 builds used installed Go 1.25.11, CGO=0, GOMAXPROCS=1, `build -p=1 -trimpath -buildvcs=true -mod=readonly -ldflags='-s -w -buildid='`. GOWORK=off, GOTOOLCHAIN=local, GOPROXY/GOSUMDB=off, GOENV=off, GOEXPERIMENT/GOFLAGS empty and pinned PATH are recorded with the complete safe process environment. Each command has a 60-second timeout and recorded 1 MiB combined output bound. Both embedded VCS records say the exact 56c revision and `modified=false`, rather than the enclosing primary checkout. No automatic build retry occurred.

All 76 embedded frontend assets in the fresh clone equal the c495 baseline and were directly compared byte-for-byte against the retained original cloud bundle. Its ZIP and inner tar SHA-256 values were verified; no asset was rebuilt, copied into the source, or substituted. The c495 baseline's 19 public payload hashes were verified. Existing clones and binaries remain untouched.

Binaries reside under `darwin-arm64/` and are excluded from the 19-member public metadata allowlist. The identity files bind their exact hashes/sizes and buildinfo. Product executables and tests were never run. This construction grants no workload, Linux-byte-equality, performance, report-validity or release qualification claim.
