# Local binary provenance correction

The local product executable c253… is embedded Go1.25.11/a804f4a5/modified=false, whereas the checked source and separately built workload CLI were79a. Intervening product changes exist. These local runs cannot validate or diagnose the79a cloud candidate. Original artifacts remain immutable; invalidation.json supersedes candidate-equivalence claims, without erasing actual cached-binary failures.

The default E2E harness rebuilds once per test process. The small diagnostic explicitly selected a prebuilt override, which checks existence but not revision. Original invocation saved a hash but omitted the override path and first upstream copy command; the precise earliest cache source is therefore not retained. All full supervisors explicitly copied local-loss-reproduction-v1/bin/wukongim. Consistent hashes and GitHEAD of a different source tree did not establish binary provenance.

No further build or run is authorized by this correction. A clean79a build plan will require exact compiler/flags from the retained cloud ELF, no cached executable, buildvcs stamp, source-clean proof and SHA binding before any new execution. Toolchain audit is delegated independently to loss_obs_ci; no host go version is substituted for executable metadata.
