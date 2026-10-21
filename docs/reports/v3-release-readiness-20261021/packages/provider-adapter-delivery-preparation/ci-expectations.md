# Fixed CI acceptance expectations

New workflow: `.github/workflows/object-storage-provider-contract.yml` (`Safety Automation - Verify Object Storage SDK Contracts`), job `provider-contract`, Ubuntu 24.04, ten-minute job limit. It is PR/main-push only, contents-read, with pinned Actions; no production credentials, environment, OIDC or manual dispatch.

1. A new virtual environment installs the unchanged complete lock with `--only-binary=:all: --require-hashes` and a 240-second limit. Missing dependency/hash/wheel installation must fail.
2. The actual SDK suite runs with a 120-second limit and must report a nonempty suite, 22 tests for this exact source, zero skips/errors/failures. It uses real SDK serialization and injected HTTP fixtures with provider sockets forbidden. Missing imports, zero tests or any skip cannot count as success.
3. The focused input suite runs with a 120-second limit; expect 10 passing tests.
4. Always-upload retains only `/tmp/provider-contract-receipt.json`, seven days; missing artifact is an error. The receipt must bind the actual checkout SHA/tree, lock hash, SDK versions and test names/counts. It is offline evidence only.

Existing repository CI remains applicable and unchanged. This local preparation did not dispatch or execute GitHub CI. Local default result is exactly 567 run: 566 pass and one existing missing-RPM-tool skip; do not report 567 passed. Source hashes are identical to the reviewed v2 tests, so no repeat was needed merely to create the local commit. After the parent's later accepted-#75/main rebase, the resulting head must be validated by its own applicable CI; prior evidence does not establish that head's Linux success.

The parent owns the later rebase/push/PR after #75 acceptance. This task performed none of those actions and does not authorize production activation, real cloud writes or a live release claim.
