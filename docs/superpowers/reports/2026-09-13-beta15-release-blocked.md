# Beta.15 publication blocked by the conversation QPS gate

The immutable tag `v3.0.0-beta.15` points to
`5b32362b712fdc94ab7d30241ac2d7f39892f469`. It is an unsuccessful publication
attempt, not an available release. Do not move the tag or reuse this failure
as release qualification. Its notes return to Unreleased on main so the
published installation guide continues to select beta.14.

## Independent exact-source evidence

Both GitHub-hosted runs used Linux AMD64, four visible CPUs, node GOMAXPROCS=2,
source/profile identity equality, and binary SHA-256
`8ade6a26623be962c443a6b88add826a64a31efd0b8cd3eb8b3a4b57c7c57438`.

| Run | Mixed list actual QPS | List P99 | HTTP request P99 | Dropped | Result |
| --- | ---: | ---: | ---: | ---: | --- |
| [Docker gate](https://github.com/WuKongIM/WuKongIM/actions/runs/34734391705) | 190.60 | 612.55 ms | 74.50 ms | 461 | Failed after all 12 base cases passed |
| [Binary gate](https://github.com/WuKongIM/WuKongIM/actions/runs/34734391702) | 199.95 | 32.52 ms | 31.78 ms | 0 | All 20 endpoint results passed |

The failing mixed list driver-wait P99 was 568.17 ms. Mixed sync passed in both
runs. Aggregate server CPU was 179.26 versus 145.26 seconds; allocated bytes per
completed request were approximately 3.11 versus 3.08 MB. Both windows had zero
HTTP errors, Channel loads/residency, and membership writes. The failing run
had about 2.7 times the aggregate hydration duration and twice the RPC duration.
These observations establish a capacity/latency discrepancy, not its root cause:
Runner CPU contention, hardware differences, and runtime contention remain
unresolved without host evidence or controlled profiling.

The failed window remains rejected. No threshold, worker count, queue bound,
admission limit, or workload was changed, and no gate was rerun until green.
The binary publisher was canceled after the independent gate, because the Docker
publisher prerequisite failed. No beta.15 Release, image, or native package was
published. Existing 90-day Actions artifacts retain the complete receipts.

## Deployment and publication disposition

The requested server's three-node Compose deployment had reverted to a beta.7
image after its newer binary bind mount was removed, producing an unknown
`gateway.token_auth_on` configuration error. Restoring the verified beta.14 image
preserved the current authentication configuration and data. All three ready
endpoints returned HTTP 200 and all containers became healthy.

Beta.14 image digest:
`sha256:f0f7c2933190a3df160b4e681437c01cf04702ebc5e4d950e335d51628949278`.
Its image revision is `47bc77ee34d6677a78dd6f8674e4293409e8f9a2`.
The original Compose file and configuration were backed up before this change.

The preparatory beta.9 package retirement publisher was canceled before sealing
or deploying. Public audit 387250694 remains the beta.14 snapshot. The unused
empty audit draft 387775814 and its bound tag are preserved. Package control is
restored to the public snapshot via PR 48; verify that PR and public status
before resuming publication. A future release needs qualified performance
and a fresh audit reservation, without changing existing immutable tags.
