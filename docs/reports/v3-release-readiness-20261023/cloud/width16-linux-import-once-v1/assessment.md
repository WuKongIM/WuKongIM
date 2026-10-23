# Linux ARM64 normal-only targeted regression

This was one new root-authorized import approach, not a retry of the legacy build. The previous60-second legacy build timeout and unexecuted runtimes remain sealed independently. No recompilation or runtime rerun occurred.

A deterministic10240-byteUSTAR held only tests/(0555) andtmp/(1777), uid/gid0,mtime0, no files or host payload. ItsSHA256 isae1c98cc869af5063bc290bdbe44701dc961ef833c427b1717a1cf3b6cdbfbdc. Import once succeeded in0.034s without builder/pull/registry/network. Local image/config digestsha256:584d68771fd6700702c4b82c821b69a977559acfb80a5a1a29a450531ceaac1a was inspected aslinux/arm64,USER65534:65534,WORKDIR/tmp,RepoDigests[]. This is a local config/image hash, not a registry manifest digest.

The already-verified staticLinuxARM64 test binaries were each mounted as one read-only file. Both retained0555 and their exact original hashes; no source/Go/module/cache/credential/socket mount was supplied. Container creation requested2CPU,2GiB+no extra swap,PIDs128,networknone,read-only rootfs,capdropALL,no-new-privileges and/tmp64MiBtmpfs mode1777.

Presence containerd122679801b7cae6a5c619b992377c8ace131761865c31c30f789a38ab0cd46a completed all6 expected terminal pass names (4top-level+2subtests), CLIexit0,StateExitCode0,OOMKilledfalse. This includes actual16/17 cohort behavior,8192keys, independent128MiB byte admission and old proof-wake/no-partial-reservation control. Cluster container0bd64c840043b2de39b39b7279ade1a3e178c07e02060f78cf4cc634c9bbca33 completed its one expected adapter16/17/cancel-reuse test, CLIexit0,StateExitCode0,OOMKilledfalse. Both ended overallPASS; names and state, not CLI alone, determine success. Runtime attach walltimes0.231s/0.210s are not performance measurements.

Exact containers were removed and inspect confirmed absence. The confirmed owned image was removed by its actualID; final oldlegacy/newimport tag query was empty and oldiid absent. A final task-label container cut was empty, all recorded process IDs were absent, exact source4cb/tree1f282 clean and all four frozen fixture hashes unchanged. No output or time limit fired.

Coverage is Linux ARM64 normal-only targeted integration. Heavy128MiB fixture ran once. Linuxrace and Linux performance acceptance remain unverified; existingMacrace evidence is separate. This does not establish message_loss rootcause or alone authorize release.
