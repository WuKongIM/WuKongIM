# Exact79a construction with original cloud flags

This is a new construction layer; the earlier unstripped construction is retained unchanged. Source is the fresh clean local clone `.worktrees/v3-rc-exact-source-79a` with a real `.git` directory. The documented worktree SHOULD deviation prevents Go1.25.11 from selecting the enclosing primary Git directory. No VCS data was fabricated and no target executable was reused.

The installed Go1.25.11 compiler, GOWORK=off, CGO_ENABLED=0, trimpath, and the cloud workflow link flags `-s -w -buildid=` were used without network access. All76 tracked Manager/Demo embedded assets were byte-equal to the retained original bundle; no asset was replaced or rebuilt. Both new Linux/amd64 executables are byte-equal to the original cloud binaries (wukongim99469fc9…eed00; wkcli43dd6555…884b). The initial unstripped executable comparison was unequal because link flags differed; that record remains unchanged.

The matching native Darwin/arm64 pair is newly built from this same clean source and flags. Its platform, shared Mac host and resource configuration remain different from the cloud environment. This construction itself started no server, workload or cloud resource and does not establish Linux performance acceptance or the cloud failure cause. The prior cached executables retain their incomplete initial construction chain; this proof does not retroactively change those runs.

Compiler, source/rule SHA manifests, exact argv, before/after clean status and initial flags are recorded in sibling exact-source-build-79a-v1; the two scopes must be read together. The source clone is retained for the next bounded local diagnostic. Binaries are intentionally outside the public allowlist.
