# Docker Compose Web Manager Auth Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `wk-web` service to `docker-compose.yml` that builds and serves the `web/` admin UI behind Nginx, proxies `/manager/*` to `wk-node1:5301`, and enables a fixed development manager login on `wk-node1`.

**Architecture:** Keep the existing Go application image untouched and add a dedicated multi-stage `web/Dockerfile` for the frontend. Route all browser traffic through a single same-origin `wk-web` container, let Nginx proxy `/manager/*` to `wk-node1`, and enable manager auth only on `docker/conf/node1.conf` with a fixed development user.

**Tech Stack:** Docker Compose, Nginx, multi-stage Docker builds, Bun/Vite frontend build, `WK_MANAGER_*` runtime config, existing `web/` SPA.

---

## References

- Spec: `docs/superpowers/specs/2026-04-22-docker-compose-web-manager-auth-design.md`
- Follow `@superpowers:test-driven-development` for each change slice, using command-level red/green checks where file-format changes are involved.
- Run `@superpowers:verification-before-completion` before claiming the deployment wiring is done.
- `docker/conf/` and `wukongim.conf.example` are config surfaces; keep comments concise and in English where comments are added.

## Critical Review Notes Before Starting

- This task is mostly infrastructure/configuration, so the TDD loop should use focused failing validation commands instead of forcing artificial unit tests. For each slice, first run a command that proves the target state is missing, then implement, then rerun it.
- `wk-web` should proxy to `wk-node1:5301` over the Compose network, so no host port mapping for `5301` is needed.
- The existing root `Dockerfile` remains the backend image build. The new frontend image must live under `web/` to keep responsibilities separated.

## File Structure

- Modify: `docker-compose.yml` — add the `wk-web` service build/run wiring and public port.
- Modify: `docker/conf/node1.conf` — enable manager auth and add the fixed development login user.
- Create: `web/Dockerfile` — multi-stage frontend build and Nginx runtime image.
- Create: `web/nginx.conf` — static file serving, SPA fallback, and `/manager/` reverse proxy to `wk-node1:5301`.
- Modify: `wukongim.conf.example` — align manager example settings and document the development-style manager login example.

### Task 1: Add the frontend container build assets

**Files:**
- Create: `web/Dockerfile`
- Create: `web/nginx.conf`

- [ ] **Step 1: Run a failing validation for missing web container assets**

Run:

```bash
test -f web/Dockerfile && test -f web/nginx.conf
```

Expected: FAIL because neither frontend container asset exists yet.

- [ ] **Step 2: Add the frontend Docker build and Nginx config**

Create `web/Dockerfile` as a dedicated multi-stage build that:
- uses a Bun image to install dependencies and run the frontend build,
- copies only the `web/` app files needed for install/build,
- emits a runtime image based on `nginx:alpine`,
- copies `dist/` and `web/nginx.conf` into the runtime image.

Create `web/nginx.conf` so that:
- `/manager/` proxies to `http://wk-node1:5301`,
- static assets are served directly,
- SPA routes fall back to `/index.html`.

- [ ] **Step 3: Re-run the asset existence validation**

Run:

```bash
test -f web/Dockerfile && test -f web/nginx.conf
```

Expected: PASS.

- [ ] **Step 4: Commit the frontend container slice**

Run:

```bash
git add web/Dockerfile web/nginx.conf
git commit -m "feat: add web container build assets"
```

### Task 2: Wire `wk-web` into `docker-compose.yml`

**Files:**
- Modify: `docker-compose.yml`

- [ ] **Step 1: Run a failing validation for the missing `wk-web` service**

Run:

```bash
python - <<'PY'
from pathlib import Path
text = Path('docker-compose.yml').read_text()
assert 'wk-web:' in text, 'wk-web service missing'
assert '8080:80' in text, 'wk-web port mapping missing'
PY
```

Expected: FAIL because `wk-web` is not in the compose file yet.

- [ ] **Step 2: Add the `wk-web` service**

Update `docker-compose.yml` to add `wk-web` with:
- `build.context: .`
- `build.dockerfile: web/Dockerfile`
- `depends_on: [wk-node1]`
- `restart: unless-stopped`
- `ports: ["8080:80"]`

Do not add a host port mapping for manager `5301`; the proxy should use the internal Compose network.

- [ ] **Step 3: Re-run the focused compose service validation**

Run:

```bash
python - <<'PY'
from pathlib import Path
text = Path('docker-compose.yml').read_text()
assert 'wk-web:' in text
assert 'web/Dockerfile' in text
assert '8080:80' in text
assert 'wk-node1' in text
PY
```

Expected: PASS.

- [ ] **Step 4: Commit the compose wiring slice**

Run:

```bash
git add docker-compose.yml
git commit -m "feat: add wk-web compose service"
```

### Task 3: Enable the fixed manager login on `wk-node1`

**Files:**
- Modify: `docker/conf/node1.conf`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Run a failing validation for missing manager auth config**

Run:

```bash
python - <<'PY'
from pathlib import Path
text = Path('docker/conf/node1.conf').read_text()
assert 'WK_MANAGER_LISTEN_ADDR=' in text, 'manager listen addr missing'
assert 'WK_MANAGER_AUTH_ON=true' in text, 'manager auth toggle missing'
assert 'a1234567' in text, 'development password missing'
PY
```

Expected: FAIL because node1 currently does not enable manager auth.

- [ ] **Step 2: Add the manager auth settings and align the example config**

Update `docker/conf/node1.conf` to include:
- `WK_MANAGER_LISTEN_ADDR=0.0.0.0:5301`
- `WK_MANAGER_AUTH_ON=true`
- `WK_MANAGER_JWT_SECRET=wukongim-dev-manager-secret`
- `WK_MANAGER_JWT_ISSUER=wukongim-manager`
- `WK_MANAGER_JWT_EXPIRE=24h`
- `WK_MANAGER_USERS=[{"username":"admin","password":"a1234567",...}]`

Grant the `admin` user `[*]` actions for:
- `cluster.node`
- `cluster.slot`
- `cluster.task`
- `cluster.overview`
- `cluster.channel`

Then align `wukongim.conf.example` with a clear manager example using the same account and a comment indicating that production deployments must replace the default secret/password.

- [ ] **Step 3: Re-run the focused manager config validation**

Run:

```bash
python - <<'PY'
from pathlib import Path
text = Path('docker/conf/node1.conf').read_text()
assert 'WK_MANAGER_LISTEN_ADDR=0.0.0.0:5301' in text
assert 'WK_MANAGER_AUTH_ON=true' in text
assert '"username":"admin"' in text
assert '"password":"a1234567"' in text
assert 'cluster.overview' in text and 'cluster.channel' in text
PY
```

Expected: PASS.

- [ ] **Step 4: Commit the manager config slice**

Run:

```bash
git add docker/conf/node1.conf wukongim.conf.example
git commit -m "feat: add compose manager login config"
```

### Task 4: Verify the Compose deployment end to end

**Files:**
- Modify: none (verification only unless a failure requires a fix)

- [ ] **Step 1: Validate the composed configuration**

Run:

```bash
docker compose config > /tmp/wukongim-compose-config.out
```

Expected: PASS with a fully rendered config that includes `wk-web` and no syntax errors.

- [ ] **Step 2: Validate the frontend image build**

Run:

```bash
docker compose build wk-web
```

Expected: PASS with a successful frontend multi-stage image build.

- [ ] **Step 3: Start the compose stack and verify the login endpoint path**

Run:

```bash
docker compose up -d wk-node1 wk-web
curl -I http://127.0.0.1:8080/login
curl -s -o /tmp/wukongim-manager-login.json -w '%{http_code}' \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"a1234567"}' \
  http://127.0.0.1:8080/manager/login
```

Expected:
- `/login` returns HTTP 200,
- `/manager/login` returns HTTP 200 and a JSON body with `access_token`.

- [ ] **Step 4: Tear down the started services and commit only if fixes were needed**

Run:

```bash
docker compose down
```

If no additional fixes were needed after verification, do not create an extra commit for this task.

## Local Plan Review Checklist

Before executing, confirm the written plan still satisfies the spec:

- [ ] `wk-web` is built from a dedicated frontend Dockerfile, not the backend root Dockerfile.
- [ ] Browser traffic stays same-origin through `wk-web`.
- [ ] `/manager/*` proxies to `wk-node1:5301` without exposing `5301` on the host.
- [ ] The fixed login is `admin / a1234567`.
- [ ] `wukongim.conf.example` stays aligned with the manager config shape.
- [ ] Final verification includes `docker compose config`, `docker compose build wk-web`, and a real login request through `wk-web`.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-22-docker-compose-web-manager-auth.md`. Ready to execute?
