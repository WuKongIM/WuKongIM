# Replay the static observation-contract check

No Go/build/backend is required. Export from `0672c115950f65e6d92381aa5b3ea9f0a20fc790` into a fresh directory:

- `.github/workflows/v3-candidate-regression.yml`
- `.github/workflows/README.md`
- `scripts/v3_candidate_workflow_contract_test.py`
- `scripts/run-v3-candidate-check.py`
- `scripts/github_workflows_test.go`

Apply only the patch test-file hunk first and run `python3 -B scripts/v3_candidate_workflow_contract_test.py -v`: expected 8 attempted, one missing-JSON assertion failure. Apply remaining workflow and README hunks, run the same command: expected 8 pass. The patch changes only three files; two supporting files are exact baseline reads. No fixture binaries, caches, checkout credentials or deployment inputs belong in this replay.

A subsequent remote run must use a new source revision. This static replay does not demonstrate runtime completion of scripts-integration.
