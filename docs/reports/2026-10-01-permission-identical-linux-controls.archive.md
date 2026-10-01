# Permission controls archive — 2026-10-01

Companion: `2026-10-01-permission-identical-linux-controls.tar.xz` (286,892 bytes, 68 regular files).

SHA-256: `671b409efe450248e16e306f99c6d5783de1347f8c75107290b33820df1042be`.

Extract into a new directory, then run `python3 verify-controls.py`. A successful evidence replay preserves the original acceptance verifier’s expected exit 1, eight A/A sequential and five A/A burst rejections, two Linux p99 increases above +5%, and no Linux native CPU qualification. It starts no server and does not replace any observation.

All six A/A reports and both Linux reports include full ACK/functional/history records. Linux reports preserve all selected histogram samples and raw labels. Frozen source/instructions, build receipts, pre-execution plans and unchanged original acceptance verifier remain in the archive. Binaries are represented by hashes/build metadata and retained separately in the local artifact directory; they are not required for offline replay.

| Member | Bytes |
| --- | --- |
| `cleanup.json` | 431 |
| `decision.json` | 510 |
| `identical/cpu-calibration.json` | 322 |
| `identical/cpu-calibration.log` | 736 |
| `identical/harness-source-binding.json` | 3,083 |
| `identical/instructions/driver/AGENTS.md` | 13,024 |
| `identical/instructions/driver/test/e2e/AGENTS.md` | 18,979 |
| `identical/instructions/driver/test/e2e/message/AGENTS.md` | 21,030 |
| `identical/instructions/driver/test/e2e/message/send_ban/AGENTS.md` | 14,313 |
| `identical/instructions/driver/test/e2e/suite/FLOW.md` | 4,293 |
| `identical/instructions/product/AGENTS.md` | 12,434 |
| `identical/new-reviewed-pair-1.json` | 614,390 |
| `identical/new-reviewed-pair-1.log` | 626 |
| `identical/new-reviewed-pair-2.json` | 614,247 |
| `identical/new-reviewed-pair-2.log` | 626 |
| `identical/new-reviewed-pair-3.json` | 614,415 |
| `identical/new-reviewed-pair-3.log` | 626 |
| `identical/old-reviewed-pair-1.json` | 613,833 |
| `identical/old-reviewed-pair-1.log` | 626 |
| `identical/old-reviewed-pair-2.json` | 613,667 |
| `identical/old-reviewed-pair-2.log` | 626 |
| `identical/old-reviewed-pair-3.json` | 614,084 |
| `identical/old-reviewed-pair-3.log` | 626 |
| `identical/product-binary-build.txt` | 8,971 |
| `identical/reviewed-pair-execution.json` | 3,369 |
| `identical/reviewed-pair-plan.json` | 1,941 |
| `identical/reviewed-pair-verdict.json` | 22,663 |
| `identical/run-identical.py` | 3,951 |
| `identical/run.log` | 10,792 |
| `identical/verify-reviewed-pairs.py` | 5,785 |
| `instruction-bindings.json` | 834 |
| `instructions/evidence/AGENTS.md` | 13,024 |
| `linux-derived.json` | 6,188 |
| `linux/artifact-failure-contract.json` | 1,099 |
| `linux/build-receipts.json` | 1,324 |
| `linux/command-plan.json` | 3,546 |
| `linux/driver-binary-build.txt` | 358 |
| `linux/driver-build.log` | 0 |
| `linux/execution.json` | 3,437 |
| `linux/new-binary-build.txt` | 8,806 |
| `linux/new-build.log` | 0 |
| `linux/new-linux-path.json` | 3,405,011 |
| `linux/new-linux-path.log` | 616 |
| `linux/old-binary-build.txt` | 8,806 |
| `linux/old-build.log` | 0 |
| `linux/old-linux-path.json` | 3,384,421 |
| `linux/old-linux-path.log` | 626 |
| `linux/plan.json` | 1,265 |
| `linux/run-linux.py` | 3,128 |
| `manifest.json` | 7,282 |
| `prior/gzip/reviewed-pair-plan.json` | 1,896 |
| `prior/gzip/reviewed-pair-verdict.json` | 23,091 |
| `prior/original/reviewed-pair-plan.json` | 1,663 |
| `prior/original/reviewed-pair-verdict.json` | 21,565 |
| `report.md` | 10,390 |
| `sources/candidate/AGENTS.md` | 13,024 |
| `sources/candidate/pkg/slot/FLOW.md` | 8,460 |
| `sources/candidate/pkg/slot/proxy/send_permission_cohort.go` | 10,579 |
| `sources/driver/fixtures/permission-cpu-darwin.c` | 2,007 |
| `sources/driver/permission_baseline_test.go` | 28,999 |
| `sources/driver/permission_cpu_probe_test.go` | 3,068 |
| `sources/driver/permission_sequential_profiles_test.go` | 1,835 |
| `sources/driver/permission_sequential_test.go` | 3,704 |
| `sources/driver/permission_timeline_test.go` | 4,452 |
| `sources/driver/suite/wkproto_client.go` | 9,739 |
| `verification-derive.log` | 277 |
| `verification-mutants.json` | 1,949 |
| `verify-controls.py` | 11,900 |
