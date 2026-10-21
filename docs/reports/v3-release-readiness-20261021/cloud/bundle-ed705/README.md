# ed705 offline bundle preparation

The credential-free [Cloud Deployment Bundle run 39555917411](https://github.com/WuKongIM/WuKongIM/actions/runs/39555917411) passed on its first attempt. Product source and the first observed/actual trusted-main control revision are both `ed705c0fbffdea485bbb65608d6e340bb049d97e`. The complete 35-run history had no reusable same-source result. Exactly one dispatch was made, with correlation-only request ID `v3-readiness-ed705-20261021`.

The runner proved both checkout identities and source reachability, rebuilt Manager/Demo and six product/control binaries, verified three native dependency checksums, sealed and independently verified the offline payload, and uploaded one content-addressed artifact. Actual job token permissions were `Contents: read` and `Metadata: read`; the frozen workflow has no Environment, cloud credentials, OIDC, procurement or activation action.

Artifact identity:

- Artifact ID: `12325112901`, expiry `2026-10-28T06:33:53Z`.
- Bundle digest: `sha256:f274ffe1a3a74278d893fd16dd3a8df2e563fda375ac0295dfd8f6f5e982900a`.
- ZIP SHA-256: `ec3c57608befe831eba0909e974836a0febecd0edc77bef39aed084cbdbe7ca7`, 141,108,564 bytes; matches the GitHub artifact API and runner upload log.
- Tar SHA-256: `3fe42fbd8367d76df7b3e85f3d96131783d374fe6cc1c6dbdd6bab4dbb74e078`, 141,450,491 bytes; matches the uploaded checksum.
- Intent SHA-256: `c0ba320bc1d656ea4b7429aa592b306434bc8dccb0fcc088a9f3238bcbb30c85`; bytes match the separately reconstructed complete DefaultIntent.

The local verifier streams the tar directly from the ZIP without extracting files or executing payload. It hashes all 110 regular files, verifies the 109-file manifest inventory and modes/sizes, recomputes the ordered bundle digest, rejects unsafe/special/duplicate paths and duplicate JSON keys, and checks all nine Linux AMD64 ELF headers. The trusted runner separately passed the full repository `seal-offline` and `verify-offline` semantics. No local build, load process, cloud call, key creation or repository edit was performed by this task.

`summary.json`, `run-evidence.json`, `job-evidence-excerpts.log`, and `verified/` contain the result. `verify-artifact.py` and `independent-review/expected-deployment-intent.json` support independent replay against a separately downloaded artifact. Raw ZIP and full job log remain under `raw/` and are deliberately excluded from the small-file archive allowlist.

This closes only offline payload preparation. It does not replace local shakeout, two-hour rehearsal, 72-hour stability or a signed production release. The top-level paid workflow must follow its fixed bundle association contract; this manual artifact is not claimed to enter that flow directly. Content addressing is proven; a second reproducible-build comparison was not run.
