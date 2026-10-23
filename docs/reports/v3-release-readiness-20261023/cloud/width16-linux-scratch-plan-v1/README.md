# Local Linux scratch recipe

Prepared only while A2 is retained. Execute only after parent independently confirms A2 terminal cleanup zero and approves execution.

This uses the exact clean4cb real-Git worktree and fixed host Go1.25.11 to crosscompile two CGO0 Linux/arm64 integration test binaries. Host compilation uses the already-populated read-only-by-policy offline module cache; the resulting scratch image and runtime need neither Go nor a module cache. No cached base image, registry or network is needed.

The five-line Dockerfile uses only built-in FROMscratch, COPY, USER65534 and WORKDIR/tmp. DOCKER_BUILDKIT=0 selects Docker27.3.1 local legacy builder explicitly, avoiding BuildKit bootstrap. This builder is deprecated; preserve any warning. If unsupported, stop; do not auto-bootstrap/pull. Build networknone/pullfalse and noRUN prevent remote package/base acquisition.

Use the exact argv/limits in recipe.json through the existing bounded subprocess supervisor. Verify static ELF64 ARM64 headers and Go build info before build. Runtime containers use actual returned local imageID, nativeLinuxARM64, no network/mounts/privileges,2CPU/2GiB/PIDs128, read-only rootfs and64MiB/tmp tmpfs. Execute the two normal-only test argv once serially. Heavy bytefixture runs once; Linuxrace is unverified.

The local imageID and source/binary hashes give reproducible provenance; local FROMscratch has no registry RepoDigest. Preserve output caps, first failure and exact-ID20s cleanup. This is functional regression, not performance acceptance or a message_loss rootcause claim.
