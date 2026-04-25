# wk-web API Proxy Config Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the `wk-web` nginx `/manager/` proxy target configurable through `WK_WEB_API_URL` while preserving the current `http://wk-node1:5301` default.

**Architecture:** Convert the nginx config into a startup-rendered template, keep the default value in the container image, and expose the variable from `docker-compose.yml` so deployments can override the manager API target without rebuilding `wk-web`.

**Tech Stack:** Docker Compose, nginx official image template rendering, static web assets.

---

### Task 1: Convert the nginx config to a template

**Files:**
- Rename: `web/nginx.conf` -> `web/nginx.conf.template`
- Modify: `web/Dockerfile`

- [x] **Step 1: Rename the nginx config into a template**

Move the existing `web/nginx.conf` contents into `web/nginx.conf.template` and replace the hard-coded `proxy_pass` target with `${WK_WEB_API_URL}`.

- [x] **Step 2: Keep a container default**

Set `ENV WK_WEB_API_URL=http://wk-node1:5301` in `web/Dockerfile` so the template renders the legacy upstream when no override is passed.

- [x] **Step 3: Switch the Docker image to nginx template loading**

Copy `web/nginx.conf.template` into `/etc/nginx/templates/default.conf.template` in `web/Dockerfile`.

- [x] **Step 4: Verify the rendered template logic**

Run a local render check that substitutes `WK_WEB_API_URL` and confirm the generated config contains the expected `proxy_pass` line.

### Task 2: Expose the variable from Compose and document it

**Files:**
- Modify: `docker-compose.yml`
- Modify: `web/README.md`

- [x] **Step 1: Add the compose environment contract**

Add `WK_WEB_API_URL=${WK_WEB_API_URL:-http://wk-node1:5301}` to the `wk-web` service in `docker-compose.yml`.

- [x] **Step 2: Document the container-side proxy setting**

Update `web/README.md` to explain that container deployments can set `WK_WEB_API_URL` to point `/manager/` at a different backend while the browser client still uses same-origin `/manager/*` calls.

- [x] **Step 3: Verify compose output**

Run `docker compose config` and confirm the rendered `wk-web` environment contains `WK_WEB_API_URL`.
