# Three-Node Docker Compose Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a reusable multi-stage `Dockerfile`, a development-oriented `docker-compose.yml`, and three node-specific config files so the repository can boot a persistent `三节点集群` locally from source.

**Architecture:** Keep the application binary and runtime semantics unchanged. Package `./cmd/wukongim` into one shared image, then run `wk-node1`, `wk-node2`, and `wk-node3` from that image with per-node `.conf` files, per-node persistent data mounts, and compose-DNS cluster peer addresses.

**Tech Stack:** Docker, Docker Compose, Go 1.23 build toolchain, existing `wukongim.conf` config loader, repo `.gitignore`

---

## File Structure Map

- Create: `Dockerfile` — multi-stage build for `./cmd/wukongim`, producing one reusable runtime image for all nodes.
- Create: `docker-compose.yml` — three-service local cluster topology, port mappings, mounts, restart policy, and shared build target.
- Create: `docker/conf/node1.conf` — node 1 config with `WK_NODE_ID=1`, compose-DNS cluster address, host-exposed dev ports, and persistent in-container paths.
- Create: `docker/conf/node2.conf` — same baseline config for node 2 with node-specific identity and cluster listen address.
- Create: `docker/conf/node3.conf` — same baseline config for node 3 with node-specific identity and cluster listen address.
- Modify: `.gitignore` — ignore `docker/dev-cluster/` runtime data so persistent volumes stay local and untracked.
- Optionally create: `docker/dev-cluster/.gitkeep` only if the repo needs the parent directory present; otherwise rely on bind-mount auto-creation.

### Task 1: Add the three node config files first

**Files:**
- Create: `docker/conf/node1.conf`
- Create: `docker/conf/node2.conf`
- Create: `docker/conf/node3.conf`
- Reference: `wukongim.conf.example`

- [ ] **Step 1: Copy the example config shape into the first node config**

```conf
WK_NODE_ID=1
WK_NODE_NAME=wk-node1
WK_NODE_DATA_DIR=/var/lib/wukongim/data
WK_STORAGE_CONTROLLER_META_PATH=/var/lib/wukongim/controller-meta
WK_STORAGE_CONTROLLER_RAFT_PATH=/var/lib/wukongim/controller-raft
WK_CLUSTER_LISTEN_ADDR=wk-node1:7000
WK_CLUSTER_HASH_SLOT_COUNT=256
WK_CLUSTER_INITIAL_SLOT_COUNT=1
WK_CLUSTER_CONTROLLER_REPLICA_N=3
WK_CLUSTER_SLOT_REPLICA_N=3
WK_CLUSTER_NODES=[{"id":1,"addr":"wk-node1:7000"},{"id":2,"addr":"wk-node2:7000"},{"id":3,"addr":"wk-node3:7000"}]
WK_API_LISTEN_ADDR=0.0.0.0:5001
WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"0.0.0.0:5100","transport":"gnet","protocol":"wkproto"},{"name":"ws-jsonrpc","network":"websocket","address":"0.0.0.0:5200","path":"/ws","transport":"gnet","protocol":"jsonrpc"}]
```

- [ ] **Step 2: Clone the config for node 2 and node 3 with only identity/listen changes**

```conf
WK_NODE_ID=2
WK_NODE_NAME=wk-node2
WK_CLUSTER_LISTEN_ADDR=wk-node2:7000
```

```conf
WK_NODE_ID=3
WK_NODE_NAME=wk-node3
WK_CLUSTER_LISTEN_ADDR=wk-node3:7000
```

- [ ] **Step 3: Keep the config intentionally minimal**

Only carry over the cluster timing fields from `wukongim.conf.example` if startup needs them. Do not add extra knobs unless the app fails without them.

- [ ] **Step 4: Sanity-check the files for exact node differences**

Run: `diff -u docker/conf/node1.conf docker/conf/node2.conf || true && diff -u docker/conf/node2.conf docker/conf/node3.conf || true`
Expected: only node identity and cluster listen address differ.

- [ ] **Step 5: Commit**

```bash
git add docker/conf/node1.conf docker/conf/node2.conf docker/conf/node3.conf
git commit -m "feat(docker): add three-node cluster configs"
```

### Task 2: Add the shared multi-stage image build

**Files:**
- Create: `Dockerfile`
- Reference: `go.mod`
- Reference: `cmd/wukongim/main.go`

- [ ] **Step 1: Prove the packaging gap before implementation**

Run: `docker build -t wukongim-dev:plan-check .`
Expected: FAIL because the repository does not yet have a `Dockerfile`.

- [ ] **Step 2: Write the builder stage with dependency-layer reuse**

```dockerfile
FROM golang:1.23 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/wukongim ./cmd/wukongim
```

- [ ] **Step 3: Add a small runtime stage that always reads the mounted config path**

```dockerfile
FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /out/wukongim /usr/local/bin/wukongim
ENTRYPOINT ["/usr/local/bin/wukongim","-config","/etc/wukongim/wukongim.conf"]
```

- [ ] **Step 4: Build the image to verify the Dockerfile is valid**

Run: `docker build -t wukongim-dev:local .`
Expected: PASS and final image contains `/usr/local/bin/wukongim`.

- [ ] **Step 5: Commit**

```bash
git add Dockerfile
git commit -m "build(docker): add runtime image for wukongim"
```

### Task 3: Add the three-node compose topology

**Files:**
- Create: `docker-compose.yml`
- Create or ensure parent exists: `docker/dev-cluster/`
- Reference: `docker/conf/node1.conf`
- Reference: `docker/conf/node2.conf`
- Reference: `docker/conf/node3.conf`

- [ ] **Step 1: Write the service skeleton using one shared build target**

```yaml
services:
  wk-node1:
    build:
      context: .
      dockerfile: Dockerfile
  wk-node2:
    build:
      context: .
      dockerfile: Dockerfile
  wk-node3:
    build:
      context: .
      dockerfile: Dockerfile
```

- [ ] **Step 2: Add per-node config mounts, persistent data mounts, and restart policy**

```yaml
    restart: unless-stopped
    volumes:
      - ./docker/conf/node1.conf:/etc/wukongim/wukongim.conf:ro
      - ./docker/dev-cluster/node1:/var/lib/wukongim
```

Use the same pattern for `node2` and `node3` with their own host directories.

- [ ] **Step 3: Add host port mappings for every node**

```yaml
    ports:
      - "5001:5001"
      - "5100:5100"
      - "5200:5200"
      - "7000:7000"
```

Node 2 should map `5002:5001`, `5101:5100`, `5201:5200`, `7001:7000`; node 3 should map `5003:5001`, `5102:5100`, `5202:5200`, `7002:7000`.

- [ ] **Step 4: Render the compose file before attempting runtime startup**

Run: `docker compose config`
Expected: PASS with all bind mounts resolved and three services rendered.

- [ ] **Step 5: Commit**

```bash
git add docker-compose.yml
git commit -m "feat(docker): add three-node compose topology"
```

### Task 4: Ignore persistent runtime data in git

**Files:**
- Modify: `.gitignore`
- Optional create: `docker/dev-cluster/.gitkeep`

- [ ] **Step 1: Add a narrow ignore rule for compose runtime data**

```gitignore
docker/dev-cluster/
```

If the parent directory itself must stay in git, prefer:

```gitignore
docker/dev-cluster/*
!docker/dev-cluster/.gitkeep
```

- [ ] **Step 2: Create the parent directory marker only if needed**

```bash
mkdir -p docker/dev-cluster
touch docker/dev-cluster/.gitkeep
```

Skip `.gitkeep` if bind-mount auto-creation keeps the repo cleaner and `docker/dev-cluster/` stays fully ignored.

- [ ] **Step 3: Verify git no longer reports runtime data noise**

Run: `git check-ignore -v docker/dev-cluster/node1 docker/dev-cluster/node2 docker/dev-cluster/node3`
Expected: each path is ignored by the new rule.

- [ ] **Step 4: Review the ignore scope**

Run: `git diff -- .gitignore`
Expected: only the Docker dev-cluster ignore rule is added; no unrelated ignore churn.

- [ ] **Step 5: Commit**

```bash
git add .gitignore docker/dev-cluster/.gitkeep 2>/dev/null || git add .gitignore
git commit -m "chore(docker): ignore local cluster data"
```

### Task 5: Run packaging and startup verification

**Files:**
- Test: `Dockerfile`
- Test: `docker-compose.yml`
- Test: `docker/conf/node1.conf`
- Test: `docker/conf/node2.conf`
- Test: `docker/conf/node3.conf`

- [ ] **Step 1: Re-render the compose file after all files land**

Run: `docker compose config > /tmp/wukongim-compose.rendered.yaml`
Expected: PASS and rendered output contains `wk-node1`, `wk-node2`, and `wk-node3`.

- [ ] **Step 2: Build and start the cluster**

Run: `docker compose up --build -d`
Expected: PASS and all three containers are created and started.

- [ ] **Step 3: Verify running state**

Run: `docker compose ps`
Expected: `wk-node1`, `wk-node2`, and `wk-node3` show `running` or healthy status.

- [ ] **Step 4: Inspect startup logs for cluster-addressing mistakes**

Run: `docker compose logs --no-color --tail=200 wk-node1 wk-node2 wk-node3`
Expected: no obvious `127.0.0.1` peer dialing errors, bind failures, or config-file read failures.

- [ ] **Step 5: Smoke-check host port reachability**

Run: `curl -sf http://127.0.0.1:5001/ || true; curl -sf http://127.0.0.1:5002/ || true; curl -sf http://127.0.0.1:5003/ || true`
Expected: either successful HTTP responses or application-level non-200 responses from the nodes, but not connection-refused errors.

- [ ] **Step 6: Stop the stack without deleting state**

Run: `docker compose stop`
Expected: PASS and containers stop cleanly while `docker/dev-cluster/node1`, `node2`, and `node3` remain on disk.

- [ ] **Step 7: Commit**

```bash
git add Dockerfile docker-compose.yml docker/conf/node1.conf docker/conf/node2.conf docker/conf/node3.conf .gitignore
git commit -m "feat(docker): add local three-node cluster stack"
```

### Task 6: Final review and scope check

**Files:**
- Review: `Dockerfile`
- Review: `docker-compose.yml`
- Review: `docker/conf/node1.conf`
- Review: `docker/conf/node2.conf`
- Review: `docker/conf/node3.conf`
- Review: `.gitignore`
- Review: `docs/superpowers/specs/2026-04-15-three-node-docker-compose-design.md`
- Review: `docs/superpowers/plans/2026-04-15-three-node-docker-compose.md`

- [ ] **Step 1: Inspect the diff for accidental runtime-code changes**

Run: `git diff --stat HEAD~1..HEAD && git diff -- . ':(exclude)docs/superpowers/specs/2026-04-15-three-node-docker-compose-design.md' ':(exclude)docs/superpowers/plans/2026-04-15-three-node-docker-compose.md'`
Expected: only Docker packaging files and `.gitignore` change; no Go production files are touched.

- [ ] **Step 2: Re-read the spec acceptance points**

Confirm the implementation preserves all approved decisions:
- shared image, three services
- per-node config files
- per-node persistent directories
- host-exposed API/TCP/WS ports for all nodes
- compose-DNS peer addresses
- three-replica cluster defaults

- [ ] **Step 3: Record any deviations explicitly before handoff**

If runtime validation required a small deviation, add a short note to the final handoff instead of silently drifting from the spec.

- [ ] **Step 4: Commit the plan document itself**

```bash
git add docs/superpowers/plans/2026-04-15-three-node-docker-compose.md
git commit -m "docs: add three-node docker compose implementation plan"
```
