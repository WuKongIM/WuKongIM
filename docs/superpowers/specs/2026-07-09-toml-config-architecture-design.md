# TOML Config Architecture Design

Date: 2026-07-09
Status: Draft for user review
Scope: `cmd/wukongim` startup configuration, Docker configuration files, example configuration, manager config snapshots

## Context

`cmd/wukongim` currently uses `wukongim.conf` files with `WK_*` `KEY=value`
entries. The same `WK_*` names are used for environment variables, and complex
list fields such as cluster nodes, gateway listeners, manager users, diagnostics
debug matches, and Prometheus scrape targets are encoded as JSON strings.

That model has been useful for early Docker and script startup, but it now has
three problems:

- Human-authored files are hard to read because structured values are embedded
  as single-line JSON strings.
- `cmd/wukongim/config.go` owns a large hand-written schema, parser, defaults,
  explicit-set tracking, and app mapping in one file.
- Config metadata is duplicated across the parser, examples, Docker files, and
  manager read-only config snapshots.

The new architecture makes TOML the only supported file format while keeping
`WK_*` environment variables as the Docker, Kubernetes, CI, and emergency
override surface.

Single-node deployments remain single-node clusters. This design does not add a
local-only startup mode or any path that bypasses cluster semantics.

## Goals

- Make TOML the primary and only runtime configuration file format.
- Keep `WK_*` environment variables as first-class overrides.
- Preserve whole-list JSON environment overrides for Docker-friendly structured
  values.
- Replace scattered config metadata with one schema that drives parsing,
  validation, examples, and manager-safe snapshots.
- Fail fast on unknown TOML keys and unknown `WK_*` environment variables.
- Keep `internal/app.Config` format-agnostic and preserve app/cluster semantic
  validation as the runtime boundary.
- Migrate repository examples, Docker configs, scripts, docs, and tests away
  from `.conf`.

## Non-Goals

- No backward-compatible runtime reader for old `.conf` files.
- No dynamic config reload or runtime mutation API.
- No element-level merge for list values.
- No change to cluster deployment semantics.
- No new global service object or broad configuration service inside
  `internal/app`.
- No exposure of raw secrets, raw environment maps, or local config file paths
  through manager APIs.

## Recommended Architecture

Add a dedicated product configuration layer outside `internal/app`. The
implementation should keep two responsibilities separate:

- a schema/loader layer that owns TOML decoding, `WK_*` environment overlay,
  source tracking, examples, and safe snapshot metadata
- an app mapper that turns normalized schema values into `app.Config`

The schema/loader layer may import small shared DTOs, but `internal/app` must
not import the TOML or environment loader. This avoids a cycle where the loader
needs `app.Config` while app-level manager snapshots need config metadata.

Startup flow:

```text
cmd/wukongim
  -> parse -config
  -> locate wukongim.toml
  -> load TOML values
  -> overlay WK_* environment values
  -> validate schema-level constraints
  -> build app.Config plus a bounded safe startup-config snapshot
  -> app.New(config)
```

Responsibilities:

- `cmd/wukongim`
  - owns CLI flags and process startup
  - calls the config loader
  - does not hand-parse individual settings
- config package
  - owns schema definitions
  - loads TOML files and `WK_*` environment variables
  - validates unknown keys, deleted keys, required values, and scalar formats
  - records source metadata and explicit-set flags
  - builds `app.Config`
  - generates safe metadata for examples and manager snapshots
- `internal/app`
  - remains format-agnostic
  - owns runtime default normalization and cross-field semantic validation
  - never reads files or environment variables directly
  - serves manager config snapshots from the bounded safe snapshot attached to
    `app.Config`, not by importing the loader

This keeps file parsing, environment parsing, and operator-facing metadata in
one place while preserving the existing composition-root boundary.

## Schema Model

The schema should describe each public startup setting once.

Each item should include:

- TOML path, such as `cluster.hash_slot_count`
- environment variable name, such as `WK_CLUSTER_HASH_SLOT_COUNT`
- value type: string, bool, int, uint, float, duration, string list, object list,
  or typed object
- default value, when the default belongs at config-load time
- required flag
- nullable flag for fields where explicit empty values are valid
- sensitive flag for secrets and credential-bearing fields
- short English label
- detailed English description for generated examples and docs
- optional deletion replacement, for removed keys
- optional validation function for schema-local range checks
- optional mapping function into `app.Config`

The schema must preserve existing explicit-set behavior. For example:

- `WK_PLUGIN_ENABLE=false` must remain distinguishable from an unset plugin
  enable key, because plugin runtime defaults to enabled unless explicitly
  disabled.
- diagnostics sampling and log compression/console flags must continue to know
  whether a value was explicitly configured.

The normalized config should retain value source metadata:

```text
default | toml | env
```

This metadata allows manager snapshots and tests to reason about effective
startup configuration without reading raw files or environment variables.

## TOML Contract

The canonical file name is:

```text
wukongim.toml
```

Default lookup order when `-config` is omitted:

```text
./wukongim.toml
./conf/wukongim.toml
/etc/wukongim/wukongim.toml
```

When `-config` is provided, only that path is read, and its contents must be
TOML.

TOML fields use structured sections and short snake_case names:

```toml
[node]
id = 1
data_dir = "/var/lib/wukongim"

[cluster]
id = "wukongim-dev"
listen_addr = "0.0.0.0:7000"
hash_slot_count = 256
initial_slot_count = 10
slot_replica_n = 3

[[cluster.nodes]]
id = 1
addr = "wk-node1:7000"

[[gateway.listeners]]
name = "tcp-wkproto"
network = "tcp"
address = "0.0.0.0:5100"
transport = "gnet"
protocol = "wkproto"
```

The TOML path and the environment key are bound by schema:

```text
cluster.hash_slot_count <-> WK_CLUSTER_HASH_SLOT_COUNT
gateway.listeners       <-> WK_GATEWAY_LISTENERS
manager.users           <-> WK_MANAGER_USERS
```

TOML uses native arrays and objects for readability. Environment variables keep
JSON strings for whole-list overrides.

## Environment Contract

Environment variables keep the existing `WK_` namespace and override TOML
values:

```text
defaults < TOML < WK_* environment variables
```

Structured list fields use JSON strings and replace the whole list:

```bash
export WK_CLUSTER_NODES='[{"id":1,"addr":"wk-node1:7000"},{"id":2,"addr":"wk-node2:7000"}]'
export WK_GATEWAY_LISTENERS='[{"name":"tcp-wkproto","network":"tcp","address":"0.0.0.0:5100","transport":"gnet","protocol":"wkproto"}]'
```

No list element merge is supported. This avoids ambiguous precedence and keeps
Docker/Kubernetes manifests easy to audit.

If no default TOML file exists, startup may still continue when environment
variables provide a complete valid configuration. This makes container images
usable in env-only deployments.

Unknown `WK_*` variables in the current process environment must fail startup.
The error should name the variable and, when applicable, suggest a replacement.
This prevents misspelled deployment settings from silently doing nothing.

## Validation And Errors

Schema-level validation should fail before any runtime starts.

Required startup keys:

- `node.id` / `WK_NODE_ID`
- `node.data_dir` / `WK_NODE_DATA_DIR`
- `cluster.listen_addr` / `WK_CLUSTER_LISTEN_ADDR`

Error rules:

- Unknown TOML path fails with the exact path.
- Unknown `WK_*` environment variable fails with the exact variable.
- Removed keys fail with replacement guidance.
- Type errors report both TOML path and environment key.
- Empty environment values are explicit empty values and are accepted only for
  nullable fields.
- Required fields fail when empty or absent after all overlays.
- Sensitive values are never included in error output.

Examples:

```text
unknown config key: cluster.hash_slots_count
unknown config env: WK_CLUSTER_HASH_SLOTS_COUNT
WK_CLUSTER_HASH_SLOT_MIGRATION_ENABLED is no longer supported; use WK_CHANNEL_MIGRATION_*
parse cluster.hash_slot_count / WK_CLUSTER_HASH_SLOT_COUNT: value must be between 1 and 65535
```

Runtime semantic validation remains in `internal/app` and lower runtime packages.
Examples include single-node cluster semantics, hash slot defaults, replica
constraints, health-report interval and TTL rules, and feature-specific runtime
defaults.

## Docker And Compose

The Docker image should default to:

```text
/etc/wukongim/wukongim.toml
```

The entrypoint should continue to start the official product binary, but the
image must not require a mounted config file when complete `WK_*` environment
variables are provided.

Repository Docker config files should migrate from:

```text
docker/conf/node1.conf
docker/conf/node2.conf
docker/conf/node3.conf
```

to:

```text
docker/conf/node1.toml
docker/conf/node2.toml
docker/conf/node3.toml
```

Compose should mount `.toml` files in file-mode examples. Env-only examples can
omit the mount and provide the required `WK_*` variables through `environment`
or `.env`.

Where practical, shared dev-cluster settings should be represented once and
per-node differences should be small. Per-node differences include:

- node ID
- external TCP address
- external WS address
- any node-specific published ports

The dev-sim hot-path tuning should remain explicit and tested as effective
loaded config, not as plain-text string checks.

## Manager Config Snapshots

The existing read-only manager node config snapshot should move toward schema
metadata instead of maintaining a separate allowlist by hand.

To avoid package cycles, the startup config loader should generate a bounded,
already-redacted snapshot from schema metadata while building `app.Config`. The
snapshot can be stored on `app.Config` as a narrow app-owned DTO. The app's
manager provider then returns that snapshot and applies only request-local
checks such as selected node ID and generated timestamp.

The snapshot remains:

- explicit inspection only
- not part of node list polling
- bounded to known schema groups
- redacted for sensitive values
- source-aware without exposing file paths or raw environment values

Sensitive fields include keys or fields containing:

```text
SECRET
TOKEN
PASSWORD
PASS
CREDENTIAL
PRIVATE
```

Known sensitive settings such as `cluster.join_token`,
`manager.jwt_secret`, and `manager.users` must be explicitly marked. Manager
users may be summarized, but passwords and password-like fields must never be
returned.

The manager API should continue returning `WK_*` keys because operators use
those names for deployment overrides. TOML paths can be added later if useful,
but this design does not require expanding the API response.

## Example And Documentation Strategy

Replace the root example:

```text
wukongim.conf.example -> wukongim.toml.example
```

The example should be human-authored at first, but tests must verify that every
TOML path in the example exists in the schema and that the example can be loaded
into a valid single-node cluster config.

Future follow-up can generate the example from schema metadata. That is not
required for the first migration as long as tests prevent drift.

Documentation updates:

- `AGENTS.md` configuration convention
- `docs/development/PROJECT_KNOWLEDGE.md`
- relevant `FLOW.md` files when they mention config loading
- Docker and script runbooks that reference `.conf`
- tests and helper scripts that point at `wukongim.conf`

## Migration Plan

Implementation should be split into focused commits.

1. Add the schema-driven config package and TOML loader.
2. Switch `cmd/wukongim` to the new loader.
3. Add coverage for TOML, environment overlay, unknown keys, deleted keys, and
   env-only startup.
4. Convert root example, Docker configs, and script configs from `.conf` to
   `.toml`.
5. Update Dockerfile and Compose mounts to use `wukongim.toml`.
6. Move manager node config snapshot metadata toward schema-driven groups.
7. Update docs and remove `.conf` assumptions from tests.

Old `.conf` files should not remain as runtime inputs. If a user points
`-config` at a `.conf` file, parsing should fail as TOML with a clear error.

## Testing Strategy

Primary tests:

- `cmd/wukongim` or the new config package parses a minimal TOML config.
- default lookup uses `./wukongim.toml`, `./conf/wukongim.toml`, then
  `/etc/wukongim/wukongim.toml`.
- explicit `-config` uses only that path.
- startup works with no config file when required `WK_*` environment variables
  are complete.
- environment variables override TOML values.
- list environment variables replace full TOML lists using JSON strings.
- unknown TOML keys fail.
- unknown `WK_*` environment variables fail.
- removed keys fail with replacement guidance.
- empty env values fail for required fields.
- sensitive values are redacted from errors and manager snapshots.
- plugin, diagnostics, and log explicit-set behavior is preserved.
- `wukongim.toml.example` loads successfully.
- all `docker/conf/*.toml` files load successfully.
- Docker config tests assert effective values for current hot-path tuning.
- `git diff --check` remains clean.

Focused verification for the first implementation slice should include:

```bash
GOWORK=off go test ./cmd/wukongim ./internal/app ./internal/usecase/management ./internal/access/manager ./docker -count=1
git diff --check
```

Broader `go test ./...` can be run once the migration touches docs, Docker,
scripts, examples, and manager snapshots together.

## Risks

- Strict unknown `WK_*` validation may fail in environments that currently set
  unrelated `WK_` variables. This is intentional, but the error must be clear.
- Removing `.conf` runtime compatibility is a breaking change. The repository
  has not stabilized this configuration surface, and the user explicitly chose
  clean migration over compatibility.
- Schema-driven mapping can become over-abstract if it tries to model every
  runtime detail generically. Keep mapping functions explicit where they remove
  ambiguity.
- Manager snapshots must not leak secrets through schema reuse. Sensitive
  marking and explicit denylist tests are mandatory.

## Acceptance Criteria

- `cmd/wukongim` no longer reads `.conf` as a supported runtime format.
- TOML is the only file format documented and tested for startup.
- `WK_*` environment variables remain the override format.
- Structured env overrides remain JSON strings that replace whole lists.
- Unknown TOML keys and unknown `WK_*` environment variables fail startup.
- Docker and script configs use `.toml`.
- `wukongim.toml.example` matches the active schema and loads successfully.
- Manager node config snapshots remain read-only, bounded, and redacted.
- Single-node deployments are still represented as single-node clusters.
