# PR #77 — first natural main checks after merge

Merged main commit `bbcfb377c7d5bb23742ebf28394a0cbc4f45cc17` has tree `0c843c1a30df5a75eac5e8ac0b966b5b84e20036`, exactly the previously accepted PR head `94d63a3c099dcaa69bcefda3b021bafcc8d828f6` tree. GitHub Git-object GET and both actual job checkout logs bind this identity; the SDK artifact independently records it.

- [Pages run 39670271788](https://github.com/WuKongIM/packages/actions/runs/39670271788): natural main push, attempt 1; 581 Linux tests, zero test skips, plus four contracts passed. Configure Pages, artifact upload, and production Deploy complete Pages snapshot were all skipped. No Pages artifact was produced.
- [SDK run 39670271825](https://github.com/WuKongIM/packages/actions/runs/39670271825): natural main push, attempt 1; 22 SDK plus 10 input tests passed, zero skips. Its 1032-byte artifact was downloaded completely; recomputed whole ZIP SHA equals the API digest and all member CRC/SHA/size checks pass. Provider network is disabled in its receipt.

The final discovery shows these two push workflows and three checks: two success and one conditional deploy skip. The unchanged Pages workflow sets deploy_bootstrap=false because preview.enabled=true; deployment requires true. Stable remains disabled. This is a production-deploy skip, not a failed or silently skipped validation test.

The credential-free native 49-case matrix was not triggered again. Its accepted first PR run remains 39668059195, bound by generation-2 archive manifest `9d34898f4d6c1a27e5302d0d1bf38fdfa63d5d65ee6714e966bddd72b420278f`; identical trees preserve its source binding. Historical generation-1 failure and unknown cause remain unchanged. No claim of production stable publication follows.

`summary.json`, `job-step-outcomes.json`, complete reviewed public job logs, source snapshots, SDK receipt and replayable `verify-postmerge.py` retain the finite proof. Private raw downloads/stderr remain in the private sibling directory. No private keys, binaries or cloud output are included. All observer processes terminated; no rerun, dispatch, cancellation, merge, source, workflow, production or cloud mutation was performed. The old task branch/worktree was not removed by this collector.
