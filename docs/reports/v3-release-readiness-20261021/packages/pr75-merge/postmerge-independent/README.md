# packages #75 post-merge verification

Read-only independent verification completed. PR #75 merged at 2026-10-21 17:22:24 UTC as `1829c3813d6b21dcfeb1238a640604a4691815bd`. It and approved tip `2368c8745853d7d8ad82e706a6e7980842526241` have identical tree `2119196cef38a159f23a8f5c47f8878e4fde8399`. The old tip is not an ancestor of the rebase merge. Its original worktree is clean; the branch and worktree were retained without any mutation. The separate earlier diagnosis worktree remains outside this cleanup scope.

[Post-merge Pages run 39643213187](https://github.com/WuKongIM/packages/actions/runs/39643213187), attempt 1, push/main, succeeded with actual checkout of the exact merge commit. Job 119371905197 completed at 17:24:43 UTC: **557 default Python tests passed, zero skips; four contract tests and the contract shell passed**. Configure Pages, upload and deployment all skipped; there were no artifacts. This run did not repeat the native 49-case client suite.

Fresh branch protection equals the parent's exact before/after responses: required strict Pages check, enforce_admins=true, force pushes/deletions forbidden, linear history and conversation resolution required. Stable remains disabled in unchanged merged controls. No workflow, Git refs, branch, worktree or provider state was changed.

The public allowlist includes bounded API responses, read receipts, complete sanitized build log, exact source contracts, local Git proof and this summary. Raw command streams remain in a private sibling directory and are excluded. No package/archive binary, credential, private key or temporary signed URL is included. Original parent merge receipts are untouched.
