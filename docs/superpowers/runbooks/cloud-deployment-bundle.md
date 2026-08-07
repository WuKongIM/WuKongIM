# Cloud Deployment Offline Bundle

`cloud-deployment-bundle.yml` is a manual, credential-free Agent Tool. It
builds one content-addressed payload before Cloud Lease Quote or Acquire.

## Identity boundary

The required `source_sha` must be a lowercase 40-character commit reachable
from `origin/main`. The Workflow keeps two checkouts:

- `control` is the exact trusted `main` revision that owns the Workflow,
  bundle validator, and host installer;
- `source` is the immutable product revision used for WuKongIM, wkbench,
  wkanalysis, Manager, and Demo.

Both identities are recorded in `deployment-intent.json` and
`bundle-manifest.json`. A moving branch name is never a bundle identity.

## Runner build

The runner rebuilds Manager with Bun 1.3.11 and Demo with Yarn 1.22.22 before
compiling the Linux AMD64 `wukongim` binary, so its embedded assets match the
source revision. It also copies both frontend distributions into the payload.
Prometheus, node_exporter, and Caddy are downloaded only on the runner and
accepted only after their pinned SHA-256 checks pass.

The target hosts receive no source, package manager, compiler, cloud token, or
mutable image reference. They receive native binaries, assets, systemd units,
configuration templates, base-image verification, and bounded evidence tools.

## Static contract

`wkcloudbundle seal-offline` refuses a payload unless it proves:

- Ubuntu 24.04 LTS, Linux AMD64, three service nodes plus one load node;
- 256 physical hash slots, 12 workload Slot Groups, and replica count 3;
- every expected executable has a Linux x86_64 ELF header and mode 0755;
- Manager and Demo entry assets, proxy, Prometheus, systemd, and evidence files
  are present;
- runtime secret paths are exact root-owned `/etc/wukongim/secrets/*` files
  with mode 0600 and no secret file is present in the bundle;
- Prometheus is fixed to 15-second scraping, 96-hour retention, and a 150 GB
  local cap, while node_exporter receives independent per-process observations;
- Demo static, API, `/route`, and WebSocket paths require the same temporary
  Basic Authentication and use distinct HTTP/WS upstream lists; and
- the complete payload contains no Dockerfile, Compose file, or native runtime
  container-engine dependency.

`wkcloudbundle verify-offline --root <directory>` independently recomputes the
canonical intent digest, every file mode/size/digest, and the ordered bundle
SHA-256. The Deployment Action must obtain the same digest on all four hosts.

The artifact contains the deterministic tar archive, its archive checksum, and
the manifest output. Its name uses the bundle digest. Artifact publication is
not cloud procurement and the Workflow has only `contents: read` permission.
