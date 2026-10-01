# Darwin WAL Sync has a runtime-trace blind spot

The Issue #977 proposal diagnosis now explains why an apparently `Running` WAL flusher is not proof of CPU work. In Go 1.25.11 on Darwin, the default full-sync path calls `runtime.fcntl` without entering the runtime syscall boundary. All twelve previously selected WAL Sync intervals have zero trace `Syscall` time. An independent file probe reproduces the visibility difference while retaining the full-sync command. This does **not** establish why candidate `ec21c1a586941ffd74cfb497f0da4522941bc1a4` regresses against old `424d03eb298b972ec572261d617972eecb5523c5`; the original six sequential failures remain.

This supplements the [exact proposal/WAL report](2026-10-01-permission-proposal-wal-stages.md). No product or dependency-cache code changed.

## Source and identity evidence

The retained Go 1.25.11 source files from the pinned build image match the official `go1.25.11` tag byte-for-byte (commit `d563bc4ba3301156c1e6b115a89c659b00d71fe7`). The chain is:

1. Pebble v2.1.4 `StandaloneManager.Create` wraps its WAL file in `vfs.NewSyncingFile`; `syncingFile.Sync` ratchets an atomic frontier and calls the underlying `SyncData`.
2. Darwin `unixFile.SyncData` calls the embedded `os.File.Sync`. Go's [`internal/poll.FD.Fsync`](https://github.com/golang/go/blob/go1.25.11/src/internal/poll/fd_fsync_darwin.go) attempts `F_FULLFSYNC`, retries `EINTR`, and falls back to `fsync` only for `ENOTSUP`.
3. [`internal/syscall/unix.Fcntl`](https://github.com/golang/go/blob/go1.25.11/src/internal/syscall/unix/fcntl_unix.go) links to [`runtime.fcntl`](https://github.com/golang/go/blob/go1.25.11/src/runtime/sys_darwin.go). That function calls `libcCall` without `entersyscall`/`exitsyscall`. The blocking syscall wrapper in the same runtime file does enter and exit that boundary.
4. Linux Pebble `linuxFile.SyncData` uses `unix.Fdatasync`, with its existing `EINTR` retry behavior. The Darwin call path and elapsed times cannot be transferred to that environment.

Pebble source was recovered from exact origin commit `2066be17d8122d056e0e1edf2d8a4213708f966c`; Git blob hashes were verified. A fresh canonical Pebble module ZIP download failed with an HTTP/2 stream error and is explicitly recorded as failed. It is not presented as a new successful ZIP verification. The probe's x/sys v0.47.0 ZIP independently passes canonical module Hash1 and matches repository `go.sum`; its generated Darwin `fcntl` uses the runtime blocking syscall wrapper. Source receipts retain versions, origins and hashes.

For each prior selected request, analysis follows its exact physical group, DB/sequence, WAL stream, record-end coverage, sync operation and goroutine. State spans must partition that one goroutine's complete Sync interval without holes or overlap. Eleven intervals are entirely `Running`; candidate/unpaced node 3 has 1.024 microseconds `Runnable`, with the remainder `Running`. All twelve have zero `Syscall` and `Waiting` time in the Sync interval. These are elapsed runtime states, not measured CPU or exclusive kernel I/O time. Prior request syscall arguments and fallback branches were not logged.

## Independent process probe

The contract and failing artifact verifier preceded the probe implementation. A source-bound Go 1.25.11 Darwin/ARM64 binary executes one fixed-order pair at `GOMAXPROCS=1`: sixteen 1-KiB writes and synchronous commits to one fresh file per phase, in the same owned directory on the Darwin host. The second phase uses `SyscallConn.Control` and x/sys `FcntlInt(F_FULLFSYNC)`, preserving descriptor lifetime, `EINTR` retry and the `ENOTSUP`-only `fsync` fallback. It does not substitute plain `fsync` as the normal command.

| Phase | Successful syncs | Sync-marker elapsed total | Trace Running | Trace Syscall | Whole probe process CPU cut |
| --- | ---: | ---: | ---: | ---: | ---: |
| Default `os.File.Sync` | 16 | 84.145 ms | 84.133 ms | 0 ms | 1.828 ms |
| Scheduled `F_FULLFSYNC` path | 16 | 63.055 ms | 0.167 ms | 62.856 ms | 2.210 ms |

The default phase spends much more elapsed time in trace `Running` than the whole process consumes in CPU. The scheduled path exposes almost all its Sync interval as `Syscall`. This confirms the observability blind spot, rather than proving a full-cluster speedup. Process CPU cuts enclose writes, markers and trace overhead; they are not per-sync CPU measurements. Trace-marker intervals also include logging overhead. Fixed order, separate fresh files and one shared host forbid a comparative performance claim from the elapsed totals.

Only the scheduled phase instruments fallback; it observed none. The default phase's raw zero-valued fallback field is a placeholder, not a measurement. The experiment does not exercise error, `EINTR`, concurrent-close or fallback branches, certify power-loss behavior, measure scheduler starvation in the original cluster, or establish whether moving the syscall boundary fixes its p99/whole-node CPU regression.

Both phases complete in one 1.074-second process invocation under a 20-second outer bound. The raw trace is 10,297 bytes, below the 8-MiB cap. All 32 begin/end identities have complete state coverage. A second independent decoder invocation reproduces the parsed text byte-for-byte; no live fixture is rerun. Eight corrupt artifact copies (missing result/marker, short phase, wrong mode, sync error, scheduled fallback, unknown state and foreign goroutine) are rejected by the CLI contract.

## Retained Linux qualification context

Read-only analysis downloaded the existing [first-attempt CI run 36759581740](https://github.com/WuKongIM/WuKongIM/actions/runs/36759581740), Artifact `11118895079`. Its source stamp is merge preview `e9909fa320e1c8a6f8be1bd8efd8ceb232078ebf`, whose tracked tree equals candidate `ec21c1a586941ffd74cfb497f0da4522941bc1a4` (`eacc295f833968c17c72a9fd8471216db43f292b`). This is the existing Linux/AMD64 mixed SEND benchmark, not a new paired four-placement experiment. It pins Go 1.25.11 and four visible CPUs/GOMAXPROCS on an observed AMD EPYC 7763 runner. The retained data filesystem is ext4; its actual mount options remain in the artifact without a device-durability claim.

Three 60-second measurement windows each complete all 30,000 scheduled arrivals at 500/s with zero errors/drops. Scheduled-to-completion p99 is 13.564, 13.828 and 12.542 ms. Exact before/after histogram identities and monotonic bucket/count/sum deltas give:

| Node | Physical commit samples | Physical commit mean | Physical commit p99 bucket upper bound |
| --- | ---: | ---: | ---: |
| 1 | 59,489 | 0.511 ms | 5 ms |
| 2 | 51,415 | 0.520 ms | 5 ms |
| 3 | 52,419 | 0.514 ms | 5 ms |

Nine lane-specific request populations have means 2.034–2.125 ms and p99 bucket upper bounds 5 ms. Request, physical-batch and Darwin selected-request populations differ; their percentile bounds are not added or compared as matched samples. Physical commit includes work beyond sync. The six retained histogram families omit WAL fsync; no exact syscall trace was exported. Host disk/pressure cuts cannot identify one proposal's I/O. Consequently this artifact supplies Linux context and its own existing qualification, but cannot locate the target WAL syscall or discharge the six original comparative failures. Local Docker is ARM64 and was used only as a pinned cross-build/decoder tool, never as the AMD64 qualification runner.

## Availability, validation and next repair boundary

During this follow-up, the earlier temporary evidence directories became unavailable. The prior selected proposal archive was recovered from Git and verified against SHA-256 `d4f4db6e0c35f26c4b1c3d9f8585c9b6404b65a9467450f777ab27f74af6e0fd`; its sixteen E2E cases and twelve positive target lineages still pass the unchanged selected-mode verifier. Historical raw-replay receipts remain, but the prior twelve raw proposal traces/full capture are unavailable locally and were not re-decoded here.

The original comparative archive was reassembled from the immutable Git parts, byte-identically matching SHA-256 `2eb068e94ce358e333bd43691d546895449a8cb907c1e12bb94f9b0b1e219c5f`. Its unchanged verifier was rerun in a fresh extraction: exit 1, six failed sequential rows and all twelve burst rows passed. No observations were replaced and no overhead or background CPU was subtracted.

The [selected follow-up bundle](2026-10-01-permission-wal-sync-visibility.tar.gz) contains the raw/parsed standalone trace, source and build bindings, prior-red/probe-pass and mutation receipts, Linux selected inputs, derived state/delta analysis and verifiers. The [archive index](2026-10-01-permission-wal-sync-visibility-archive.json) binds its hashes and a larger persistent local bundle containing the native binary, full x/sys ZIP, original comparative archive, prior selected archive and original Linux Artifact ZIP. The larger bundle contains all inputs for this follow-up; it does not recreate missing prior raw traces.

For offline validation, extract the unchanged prior `2026-10-01-permission-proposal-wal-diagnostics.tar.gz` into `prior/` and this selected bundle into `followup/`, then run `python3 followup/verify-sync-evidence.py prior`. Both follow-up archives round-trip byte-identically and pass that verifier in independent extraction directories. To independently decode the new standalone raw trace, use Go 1.25.11 `go tool trace -d=parsed probe.trace` and compare the recorded decoded SHA; all retained source/build versions remain exact.

The named local Go-format and FLOW-contract checks pass. The first FLOW check failed because the restored private offline cache lacked pinned goldmark v1.8.2; restoring that exact dependency allowed the same check to finish. Both outcomes and logs are retained separately from the immutable diagnostic bundles. No failed workload was repeated or replaced.

The next isolated repair experiment should change only the Darwin full-sync scheduling boundary in an owned candidate copy, retain command/error/fallback and synchronous publication semantics, and exercise those currently untested failure paths before implementation. It must then run the full original paired p99/whole-node CPU experiment and exact-source Linux500 gate. Linux attribution additionally needs matched WAL syscall evidence; its current aggregate counters cannot supply that identity. This report changes neither product code nor qualification gates, and makes no fix, merge or release claim. Documentation-only scope continues to justify `skip-changelog`.
