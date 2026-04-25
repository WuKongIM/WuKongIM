# Three-Node Docker Compose Design

## Summary

Add a development-oriented three-node Docker Compose deployment for the current
WuKongIM repository.

The deliverable should let contributors build the current repository into a
single runtime image, then start a `三节点集群` locally with persistent per-node
state, explicit per-node ports, and node-specific config files that follow the
existing `wukongim.conf` + `WK_` configuration convention.

This is a packaging and runtime-assembly change only. It should not require
production code changes to cluster bootstrapping, config parsing, or app
wiring.

## Goals

- Provide a repository-local `docker-compose.yml` that starts three WuKongIM
  nodes from the current source tree.
- Add a reusable multi-stage `Dockerfile` that builds the existing
  `./cmd/wukongim` binary.
- Keep the deployment aligned with current cluster-first semantics: one
  deployment is always a cluster, and this setup specifically targets a
  `三节点集群`.
- Preserve the repository's existing config model by using `KEY=value` config
  files with `WK_` prefixes instead of inventing a compose-only config format.
- Make development and debugging easy by exposing API, TCP, and WS ports for
  each node directly on the host.
- Persist node data on disk so cluster state survives container restarts.

## Non-Goals

- No reverse proxy, ingress gateway, load balancer, or service mesh.
- No monitoring stack such as Prometheus, Grafana, or log shipping.
- No production hardening such as TLS, secrets management, resource quotas, or
  orchestration-specific tuning.
- No bootstrap sidecar or custom init job that mutates cluster metadata after
  startup.
- No changes to existing Go runtime semantics, cluster membership code, or app
  dependency wiring.
- No attempt to support both development and production deployment styles in the
  same first version.

## Current State

### Repository support that already exists

- `cmd/wukongim` already loads config from `wukongim.conf`-style files and
  supports an explicit `-config` flag.
- The config layer already accepts cluster settings through `WK_` environment or
  file-based keys, including `WK_CLUSTER_NODES`, replica counts, storage paths,
  and listener definitions.
- The repository already treats a single process as a `单节点集群`, so a compose
  deployment should remain fully cluster-first rather than introducing a local
  bypass mode.

### Gaps this design fills

- There is no repository `Dockerfile` for building a runtime image.
- There is no checked-in `docker-compose.yml` for local multi-node startup.
- There are no dedicated node-specific config files for a three-node Docker
  deployment.
- There is no documented, repeatable local workflow for starting a persistent
  multi-node cluster from the current source tree.

## Approaches Considered

### 1. Recommended: Multi-stage Dockerfile + compose + per-node config files

Build one reusable image from the repository source tree, then run three
services that each mount their own config file and data directory.

Pros:

- matches normal deployment shape more closely than `go run`
- keeps compose readable because large `WK_` values stay in config files
- makes per-node differences explicit and easy to debug
- avoids touching application code
- starts faster after the image is built once

Cons:

- adds several small config files
- requires one more build artifact than an env-only compose file

### 2. Compose-only with all configuration inline in `environment`

Encode every node's config directly in compose service definitions.

Pros:

- fewer files
- quick to prototype

Cons:

- poor readability for `WK_CLUSTER_NODES` JSON and listener arrays
- harder to compare or edit node-specific settings
- easier to make quoting mistakes in compose YAML

### 3. Development-only `go run` containers

Run `go run ./cmd/wukongim` inside containers with the source tree mounted.

Pros:

- minimal packaging setup
- convenient for rapid code iteration in some workflows

Cons:

- slower startup
- larger runtime surface because build tooling must remain in the container
- less representative of a real deployment artifact

## Recommended Approach

Choose approach 1.

The repository should gain a reusable build artifact and a development-focused
compose topology that are both explicit and boring:

- one `Dockerfile` at the repository root
- one `docker-compose.yml` at the repository root
- three node config files under a docker-specific config directory
- three persistent host directories for node state

This keeps the change self-contained, readable, and aligned with the current
project architecture.

## Design

### 1. Files to Add

Add the following files:

- `Dockerfile`
- `docker-compose.yml`
- `docker/conf/node1.conf`
- `docker/conf/node2.conf`
- `docker/conf/node3.conf`
- optionally a short `docker/README.md` only if the compose file comments are
  not enough

No existing runtime code should need to change for this first version.

### 2. Image Build Strategy

The `Dockerfile` should be a multi-stage build.

#### Builder stage

- use a Go image compatible with the repository toolchain (`go 1.23.x`)
- set the working directory inside the container
- copy `go.mod` and `go.sum` first, then download dependencies for layer reuse
- copy the repository source
- build `./cmd/wukongim` into a single binary such as `/out/wukongim`

#### Runtime stage

- use a small, conventional Linux runtime image rather than an unusual base
- include CA certificates if needed by future outbound clients
- copy in the built binary
- use a simple entrypoint or command that runs the binary with
  `-config /etc/wukongim/wukongim.conf`

The image should be generic: the same image serves all three nodes, while node
identity and ports come from mounted config files.

### 3. Compose Topology

The compose file should define three services:

- `wk-node1`
- `wk-node2`
- `wk-node3`

All three services should:

- build from the repository root using the shared `Dockerfile`
- join the same default compose network
- mount one node-specific config file to `/etc/wukongim/wukongim.conf`
- mount one node-specific persistent host directory to hold data and logs
- restart with `unless-stopped`

The containers should communicate with each other by compose DNS names, not by
host IPs.

### 4. Cluster Addressing Model

Cluster membership should be fixed in each config file through the same logical
node list:

```json
[
  {"id":1,"addr":"wk-node1:7000"},
  {"id":2,"addr":"wk-node2:7000"},
  {"id":3,"addr":"wk-node3:7000"}
]
```

This is the key design decision for inter-node addressing:

- cluster traffic uses container-internal DNS names
- host port mappings exist only for development access from the host machine
- no config should depend on the host's LAN IP or Docker bridge IP

Each node's `WK_CLUSTER_LISTEN_ADDR` should therefore be its service name plus
cluster port:

- node 1: `wk-node1:7000`
- node 2: `wk-node2:7000`
- node 3: `wk-node3:7000`

This ensures that cluster peers can dial each other inside the compose network.

### 5. Replica and Cluster Semantics

The compose deployment should boot as a real three-node replicated cluster.

Config values should therefore default to:

- `WK_CLUSTER_CONTROLLER_REPLICA_N=3`
- `WK_CLUSTER_SLOT_REPLICA_N=3`

Other cluster timing values can stay close to the current example config unless
there is a Docker-specific need to override them.

This keeps the deployment consistent with the repository's cluster-first model
instead of using a one-node example config copied three times.

### 6. Per-Node Port Layout

Because this setup targets development and debugging, each node should expose
its own API, TCP, and WebSocket listener to the host.

Recommended host port mapping:

- `wk-node1`
  - API: `5001 -> 5001`
  - TCP: `5100 -> 5100`
  - WS: `5200 -> 5200`
  - Cluster: `7000 -> 7000`
- `wk-node2`
  - API: `5002 -> 5001`
  - TCP: `5101 -> 5100`
  - WS: `5201 -> 5200`
  - Cluster: `7001 -> 7000`
- `wk-node3`
  - API: `5003 -> 5001`
  - TCP: `5102 -> 5100`
  - WS: `5202 -> 5200`
  - Cluster: `7002 -> 7000`

Inside the containers, the app can keep a consistent internal listener layout:

- API listens on `0.0.0.0:5001`
- TCP gateway listens on `0.0.0.0:5100`
- WS gateway listens on `0.0.0.0:5200`
- cluster listens on `<service-name>:7000`

This gives each container the same internal wiring while still making every node
reachable from the host on a unique external port.

### 7. Config File Layout

Each node config file should stay in the project's existing `.conf` format and
share the same structure, differing only where node identity or ports require
it.

Each file should define at minimum:

- `WK_NODE_ID`
- `WK_NODE_NAME`
- `WK_NODE_DATA_DIR`
- `WK_STORAGE_CONTROLLER_META_PATH`
- `WK_STORAGE_CONTROLLER_RAFT_PATH`
- `WK_CLUSTER_LISTEN_ADDR`
- `WK_CLUSTER_HASH_SLOT_COUNT`
- `WK_CLUSTER_INITIAL_SLOT_COUNT`
- `WK_CLUSTER_CONTROLLER_REPLICA_N`
- `WK_CLUSTER_SLOT_REPLICA_N`
- `WK_CLUSTER_NODES`
- `WK_API_LISTEN_ADDR`
- `WK_GATEWAY_LISTENERS`
- optional log directory settings if helpful for inspection

Paths inside the container should point into the mounted persistent directory,
for example under `/var/lib/wukongim`.

### 8. Persistent Directory Layout

Host-side persistent directories should be separated per node so state can be
kept, inspected, or deleted independently.

Recommended host paths:

- `./docker/dev-cluster/node1`
- `./docker/dev-cluster/node2`
- `./docker/dev-cluster/node3`

Inside each container, these can be mounted to a common path such as
`/var/lib/wukongim`.

Each node config should then place node-local data beneath that mount, for
example:

- `/var/lib/wukongim/data`
- `/var/lib/wukongim/controller-meta`
- `/var/lib/wukongim/controller-raft`
- `/var/lib/wukongim/logs`

The reset story should remain simple: deleting the corresponding host
subdirectories resets the cluster state.

### 9. Operations and Developer Workflow

The first version should support the following workflow cleanly:

```bash
docker compose up --build -d
```

Expected usage model:

- inspect node logs with `docker compose logs -f wk-node1` and similar
- call node APIs through `localhost:5001`, `localhost:5002`, `localhost:5003`
- connect gateway clients to `localhost:5100/5101/5102`
- connect WebSocket clients to `ws://localhost:5200/ws`, `ws://localhost:5201/ws`,
  and `ws://localhost:5202/ws`
- stop without data loss using `docker compose stop`
- fully reset by deleting `docker/dev-cluster/node1`, `node2`, and `node3`

### 10. Error Handling and Recovery Expectations

This design intentionally favors debuggability over automation.

- compose should restart containers automatically after crashes via
  `restart: unless-stopped`
- persistent directories keep state after container restart
- no automated cluster heal or metadata cleanup is part of this first version
- if a developer wants a fresh cluster, they should remove the persistent data
  directories explicitly

### 11. Validation Strategy

Validation for this task should stay scoped to the packaging change.

At minimum:

- verify the `Dockerfile` builds the project binary successfully
- verify `docker-compose.yml` parses successfully
- verify the compose config renders correctly via `docker compose config`
- verify the generated config files are mounted and referenced correctly
- if practical in local runtime budget, start the stack and confirm the three
  containers enter a running state

Repository-wide integration tests are out of scope for this packaging change.

## Affected Areas

### New packaging artifacts

- `Dockerfile`
- `docker-compose.yml`
- `docker/conf/node1.conf`
- `docker/conf/node2.conf`
- `docker/conf/node3.conf`

### Optional documentation touchpoints

- `wukongim.conf.example` only if we decide to reference the Docker cluster
  deployment there, though this is not required for the first version
- a small Docker usage note if the compose file comments are insufficient

## Open Questions

There are no remaining product-level open questions for the approved first
version.

Implementation may still choose small operational details, such as the exact
runtime base image or whether to include a healthcheck, as long as it preserves
these approved design decisions:

- shared image, three services
- per-node config files
- per-node persistent directories
- host-exposed API/TCP/WS ports for all nodes
- cluster peer addressing through compose service names
- three-replica cluster semantics by default

## Testing Strategy

### Packaging checks

- `docker compose config`
- `docker build` through the compose build or direct Dockerfile build

### Runtime smoke checks

- start the three-node stack locally
- inspect container status with `docker compose ps`
- inspect startup logs for obvious config or bind failures

### Manual verification targets

- each node API is reachable on its mapped host port
- each node gateway listener is reachable on its mapped host port
- cluster peers do not attempt to dial `127.0.0.1` or host-only addresses
- data persists across `docker compose stop` and `docker compose up`
