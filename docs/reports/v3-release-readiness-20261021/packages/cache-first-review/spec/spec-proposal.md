# Proposed cache persistence repair — scope for review

The CI cache failure is a harness persistence-coverage defect. Both exact digest-pinned native images reproduce a split path using the unchanged DNF cache/installroot options: final main and libdnf repository cache use `/tmp/dnf-cache`, while RepoPersistor retains `/tmp/client-root/tmp/dnf-cache/expired_repos.json`. Current CI mounts only the latter and its installroot persistdir. Thus native repository cache is outside the host bind and disappears with each client container. The observed 19-byte global file is not metadata and must continue to fail the metadata/reuse gate.

Native configuration evidence is in `native-path-proof.json`, `config-probe-v4.py.stdout` and `config-probe-alma.py.stdout`. `Cli.configure` in each pinned image applies options, reads configuration and prepends installroot, activates the persistor, configures repos, then reapplies options. The repository's live parent configuration follows the final CLI directory; the early persistor retains its captured string. `base.close()` writes the actual expired_repos.json at that early location. `ci-client-mounts.json` binds this observation to the failing command164 mount list.

This confirms native configuration/lifecycle and the missing bind. It does not replay a signed DNF download or prove metadata cache reuse: those require the unchanged real 49-case workflow after the repair. Probe v1/v2 failed due to introspection of unavailable Python attributes, are retained, and make no behavior claim. Successful v3/v4 and Alma use actual installed DNF/libdnf, network none, read-only rootfs and bounded tmpfs. All probe containers were removed by exact generated label/name, with empty final inventories. No signing keys or production data were read.

## Narrow implementation

Keep the existing two installroot mounts so all previously preserved state remains available. Add separate host cache subdirectories mounted at the two final CLI locations:

| Host scratch child | Client destination | Purpose |
|---|---|---|
| dnf-cache | /tmp/client-root/tmp/dnf-cache | Existing early persistor state |
| dnf-state | /tmp/client-root/tmp/dnf-state | Existing early installroot state |
| dnf-cli-cache | /tmp/dnf-cache | Actual native repository metadata and solver cache |
| dnf-cli-state | /tmp/dnf-state | Final CLI persistent data |

Use distinct host children; do not alias two native directories onto one host directory and thereby merge distinct client state. Extend the RPM metadata classifier to recognize only the same metadata forms under both cache roots (`repodata/`, `.solv`, `.solvx`). Do not classify expired_repos.json, package RPMs, arbitrary state, empty files or partial APT lists as metadata. Existing bounded inventory, root helper, epoch arming, unchanged inode/content and actual atime-read proof remain intact. No changes to keepcache=False, metadata_expire=0, makecache --refresh, download-only order, generation barriers, signatures, version/source checks, or the 49 native cases. Four bind mounts remain inside the task's one owned scratch cache subtree and reuse the existing exact cleanup helper.

Expected files: driver and its tests only, plus a concise operator-facing CHANGELOG/documentation correction if required. Production validator, image pins, signed producers, package archives, planner and workflows stay byte-identical. The existing driver comment claiming all DNF directories are installroot-relative must be corrected.

## Evidence and alternative hypotheses

- Global/repository path split: confirmed by actual native configuration and close-time write on both target images.
- keepcache=False deleting metadata: no supporting evidence; [DNF's configuration reference](https://dnf.readthedocs.io/en/stable/conf_ref.html#keepcache-label) describes downloaded-package retention. More importantly the missing mount is already proven without changing keepcache. The next native cache cases must still demonstrate persisted metadata/read behavior.
- Helper silently dropping metadata: inconsistent with complete inventory semantics; every nonempty regular file is returned before classification and invalid files/links/permission errors fail explicitly. The only supplied file is correctly rejected. Do not weaken this gate.

The earlier docs' broad installroot description is insufficient to determine these exact image implementations. [DNF command reference](https://dnf.readthedocs.io/en/stable/command_ref.html) and [upstream configuration code](https://github.com/rpm-software-management/dnf/blob/master/dnf/conf/config.py) are orientation only; pinned image source and native results are the deciding evidence.

## Validation boundary

All failure modes in `failure-inventory.md` must become tests before implementation. Record the old-source red, then change only the harness mount/classifier boundary. Run relevant local tests, independent review and the next exact-head first CI. On native cache-A-first require nonempty actual metadata. On native cache-A-repeat require same inode and bytes read after epoch arming; no mere file-presence fallback and no requirement of zero network requests. Cache-B-target must still validate B. Retain any new first failure, do not auto-retry, and do not claim the unexecuted 25 cases passed.
