# Offline object-storage planner handoff

Base: `782c14602a6b243d893e6abf4d1682b7370736c4`; branch `codex/object-storage-plan`.
The worktree remains uncommitted and unpushed for root review. Only five files differ; publisher code, workflows and production manifests are unchanged.

The CLI inventories independently anchored canonical preview `add_release` snapshots, checks byte/metadata dependency closure for every supplied generation, classifies floating public entrypoints separately from immutable dependencies, and emits a deterministic retain-all plan with no delete actions. It explicitly rejects stable/multiple channels and native zstd metadata. Signatures, GitHub provenance, control-commit provenance, cloud state and activation remain unverified/unauthorized in the output. It is not a stable publisher.

Validation:

- Initial failure-first inventory and 27 tests preceded implementation (`02-complete-red.log`).
- Review found pre-hash size checks were too late. Three regression tests with nine failing subcases were recorded before the local planner fix (`06-resource-bounds-red.log`). No existing shared helper changed.
- `python3 -m unittest tests.test_plan_object_storage_publication tests.test_archive_package_snapshot tests.test_package_audit_receipt tests.test_compose_package_site -v`: 83 tests pass, including 30 planner tests (`07-resource-bounds-related-green.log`).
- `bash tests/contract.sh`: passes (`08-contract.log`). `git diff --check`: passes.
- Two actual CLI invocations against a two-generation TEST ONLY fixture produce identical plan bytes and preserve every input file (`cli-acceptance.json`). Plan SHA-256: `badfe3ff227bfa504835a9f6e497bf7877619cf8de0a3a72beee7dd9cd09aa14`.
- Independent reviewer `ci_audit` confirmed the resource issue is closed and found no other blocking finding. Its final report and real public beta.24 compatibility evidence are separate sibling directories managed by those agents.

Reviewable patch: `review.patch`; file hashes and scope: `implementation-summary.json`.
The fixture contains synthetic public certificates and signatures only, no private keys, and does not establish cryptographic publication readiness.
