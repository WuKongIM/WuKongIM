# First local Linux scratch attempt

The two exact-source Go1.25.11 crosscompiles succeeded once: presence12.448s, cluster28.042s, stderr empty. Both binaries are staticELF64/AArch64 with noPT_INTERP/PT_DYNAMIC, chmod0555 with byte hashes unchanged, actual build infoGo1.25.11/GOOSlinux/GOARCHarm64/CGO0/vcs4cb45b3409b155ca6a0eeb3d72d1b05bbee817ac/vcs.modified=false.

The one explicitly local legacy FROMscratch build failed at its60-second outer bound:60.023s, exit-9 after ownedPGID15577 kill. Output was zero stdout and209-byte legacy deprecation warning. No build step or successful imageID was returned. This evidence does not identify why the build stalled. No retry, builder fallback, pull, remote base or network acquisition occurred.

All dependent execution stopped. No container was created/started and neither targeted Linux test group ran. Linux integration and Linuxrace remain unverified; priorMacrace is separate. This is a local build-path failure, not a product test verdict or proof of message_loss cause.

Bounded after-failure checks found no task image tag, noimage.iid, no task-labeled containers and none of the five recorded process IDs. The exact real-Git source remains4cb/tree1f282 clean. The absence cuts are retained; no claim is made about unobserved daemon internals.

The unique local scratch approach needs no cached base image. Missing official base cache is not an external blocker for that approach. Any subsequent build or test attempt requires parent direction; first failure is retained.
