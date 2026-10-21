# Initial hypotheses — before native file-type probes

Exact source de43579a9fa88bf70f64e6dcd926a2ddf6057496. No shipping changes, CI retry or cloud/SSH access.

1. Public repository-key import creates transient special files (e.g. agent sockets) beneath the actual DNF cache. Prediction: calling the pinned DNF public-key import path in an empty scratch cache produces a non-regular lstat entry while ordinary metadata remains bounded. This is an unconfirmed lead, not a CI root-cause claim.
2. Native cache creation uses hardlinks. Prediction: all non-directory entries are regular, but one has st_nlink > 1; file-type checks alone would not explain rejection.
3. A normal cache file exceeds 64 MiB. Prediction: the failed inventory contains a regular single-link file above that exact limit; key import alone may not reproduce it.

The first CI did not report path/type/nlink/size. Original files are gone after verified owned cleanup, so a local minimal replay can identify a concrete matching mechanism but cannot recover the original CI stat retroactively. No configuration/source inference counts as native cache recovery.
