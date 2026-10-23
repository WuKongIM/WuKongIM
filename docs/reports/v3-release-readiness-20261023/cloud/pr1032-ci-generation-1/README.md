# PR1032 first automatic CI

Head `b47c03d461a6d8918e537e027bf56539ce78c585` and actual checkout `b6007c97971b5b3645b8917fc5a14c9499d0cfed` share tree `7ce68e726fea61bd6d176fbc7053dede920d5b00`. Checkout parents are exact base `364253b3be532acb023757791a54b85d4c76cbd2` and head; 14 completed jobs independently log that checkout. Base/head rules, workflow contracts and policy are byte-identical and rehashed after collection. No actual AGENTS/FLOW source file is archived.

Four natural runs (candidate39884710344, three-node39884710345, native39884710282, signal39884710379) succeeded on first attempt: 16 successful jobs and 2 explicit non-PR skips. Final read still shows draft. The three recorded protected Review Agent workflows remained disabled.

Default passed 308.746s: 219 package PASS and 15 no-test-file packages. Scripts passed 570.254s: root PASS 651, SKIP 6; all-level PASS 1317, SKIP 6; package PASS 7, zero FAIL/unfinished. Original base-policy argv/cwd, 1800s/600s outer limits, 8MiB cap, GOFLAGS=-p=2 (scripts additionally -json), Go9m/-parallel2 were verified. Test names are not filtered for Token/Secret/Authorization. Retention, nine registration-signal cases, original interrupt and direct-lab Start passed; inner 79 case receipts were not separately uploaded.

One fixed E2E binary `bc7cd8f479d2cf7a08d07809a560e514832f76da4dd42ac8f5bd95d0fcfc105a` passed all five migration topologies, 55 history checks (1 temporary retries), cross-node messaging, and self-chat with 1/3 nodes and one full-cluster restart each. Auth refusal and sender suppression passed. Optional 2000QPS comparison is skipped. All six named-check/build/scenario owned-SID cleanup receipts are verified, complete and have no remaining processes.

All three 500QPS seams each completed three 60s measurement windows: 270000 measured planned/started/completed, zero errors/drops, with 90000 separate warmup requests. P99 maxima: tcp-sendack 178.222819 ms, channel-append 8.170984 ms, mixed-send 18.279301 ms; unchanged limit400ms. Exact counter windows, Go1.25.11, four visible Linux AMD64 CPUs and binary/source identities are retained. Correctness separately reports sent=75502, acknowledged=75502, outcome=operator_stop, cause=operator_requested; its large artifact was not downloaded.

Native build/install, TEST ONLY signed-repository validation and real-systemd lifecycles passed on Ubuntu24.04, Debian12, Rocky9, Alma9. The wrapper suppresses inner success output: this archive binds actual enabled commands/steps and Go summaries, without inventing independent container-zero proof. Optional GoReleaser cosign verification was unavailable. This is not native-channel publication.

Six small ZIPs total 413165B with 80 UTF8 members; full API size/SHA and CRC/path/type checks passed. All original ZIPs, raw logs and self-chat identities stay private. Metadata-only bodies: three-node-pr-1032-b6007c97971b5b3645b8917fc5a14c9499d0cfed-1 229432588B, wukongim-native-package-preview-39884710282 77908100B. Only typed public evidence and hashes are included.

No dispatch/retry/cancel/enable/comment/ready/merge, product/source execution, Go test, provider or cloud operation occurred. Local observer schema mistake is preserved separately. Passing this PR does not establish GA or 72-hour stability. All auditor subprocesses are terminal.
