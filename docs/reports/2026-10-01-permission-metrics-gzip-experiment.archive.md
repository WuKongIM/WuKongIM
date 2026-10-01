# Metrics gzip experiment archive

Selected archive: `2026-10-01-permission-metrics-gzip-experiment.tar.xz`

- SHA-256: `0b906e6518dfbf5417ca794043c67e51fe4b58ff76fb25d559be03339fc9b29f`
- Bytes: 164,536
- Members: 60
- Measured candidate: `38d53d3dc555361e1b72bde6ea49a6dd34586f17`; parent `c03fa6cc1712e886d9359822e23be716fdb4534e`.
- All six fresh reports, original unchanged verifier, raw CPU/ACK/count/history evidence, all three benchmark samples per handler, pre-code failure contracts and exact measured source are included. Prior original verdict/source bindings are included; its six earlier reports remain in the quorum-boundaries archive.
- Binaries/toolchain are excluded. Exact native candidate binary is retained under `/Users/tt/.codex/artifacts/issue-977-metrics-gzip-20261001`; old binary/driver/probe remain in the prior Darwin artifacts.

Extract into a disposable directory and run `python3 verify-experiment.py`. Positive archive replay passed, with unchanged acceptance verifier exit 1, nine sequential rejected rows and twelve burst passes. Six rehashed semantic mutations were rejected. The initial helper compared a source-file hash with a binary hash; the failed helper/log and corrected distinction are retained. Measurements were not altered. No performance repair or Linux capacity qualification is certified.
