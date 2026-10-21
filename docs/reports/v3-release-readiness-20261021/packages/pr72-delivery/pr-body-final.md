Adds a first-generation stable offline candidate using the existing package planner, preparation, composer, audit and independent APT/RPM verification engines. Candidates bind external public control/source bytes, preserve original evidence, retain null publication IDs and are rejected by the default publication consumers.

Validated at `c8aae37d13787a0aceceac751d6d35bbc1d89c35`:
- [Pages contracts](https://github.com/WuKongIM/packages/actions/runs/39579343465): 449 Python tests and 4 contract tests passed; deployment skipped.
- [Native TEST ONLY drill](https://github.com/WuKongIM/packages/actions/runs/39579343148): all 31 candidate cases (10 positive, 21 exact rejection checks), metadata 7, local signing 10, bootstrap preparation 6, and the complete signed package repository build/install validation passed. Candidate verification runs after test-secret removal; scratch cleanup and unchanged control bytes are verified.

Stable remains disabled. This supports only the first `add_release`; it does not publish, activate channels, use production private keys, or establish public client/CDN acceptance. Rotation, retention, sealing and object-storage activation remain separate work. Thirty protected control/signing/archive files retain their base bytes.

[The first native failure](https://github.com/WuKongIM/packages/actions/runs/39578472435) is retained without rerun. The disposable GnuPG directory was shortened after reproducing a Unix socket path limit locally; the corrected commit passed the complete native path above.
