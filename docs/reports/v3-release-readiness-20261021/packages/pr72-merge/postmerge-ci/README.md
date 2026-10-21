# PR #72 postmerge CI

Run [39582962603](https://github.com/WuKongIM/packages/actions/runs/39582962603), attempt 1, completed successfully at 2026-10-21 10:14:57 UTC. It was the automatic push run for merged commit `3fe04138403a7c9443e4ede457dccfa16dff2b00`. The complete validation-job log records checkout of that exact commit. Local Git objects independently prove its tree `5fac246f4234d1652b86f26a72927a4b003bbbe7` equals the approved `c8aae37d13787a0aceceac751d6d35bbc1d89c35` tree.

- Contract shell completed, including 4 tests / OK (0.001 seconds).
- Full Python discovery completed: 449 tests / OK, no skips (95.284 seconds).
- Configure Pages, Upload complete Pages artifact, and the entire Deploy complete Pages snapshot job were skipped. Artifact API reports zero artifacts. No publication occurred in this run.
- Complete job log: `05-complete-job-log.log` (18,951 bytes, 194 lines). Expected negative-fixture receipts printed by unittest include missing-tool and simulated signer/preflight failures; source tests explicitly assert those failures. They are not native CI failures and this postmerge Pages run does not rerun the full native drill.

The original approved tip is not an ancestor of the rebase merge, although the trees match. The original worktree is clean and retained, as is its branch; no forced cleanup was performed. No dispatch, rerun, comment, cloud operation or repository mutation was performed by this audit. All six GitHub GET reads in this audit succeeded; there was no transient EOF to classify as a CI failure. Earlier evidence is untouched.

`allowlist.txt` identifies the public archive files; `sha256.json` hashes each allowlisted file. Raw API responses, stderr/command receipts, complete job log, exact workflow source and Git-tree proof are retained. No ZIP, binary package, credential or private-key material is included.
