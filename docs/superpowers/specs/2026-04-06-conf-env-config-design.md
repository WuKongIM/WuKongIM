# Conf And Environment Config Design

## Overview

Replace the current `-config <json>` startup model with a `.conf`-based configuration system that uses `KEY=value` entries matching environment variable names.

The new configuration contract should satisfy two operating modes:

- local or service startup can rely on a default config file path without passing `-config`
- deployment systems can override the same keys via environment variables

This change is a direct cutover. The repository does not keep JSON or YAML compatibility.

## Goals

- Make `wukongim.conf` the primary configuration format
- Use one shared key namespace for config files and environment variables
- Support startup without `-config` by scanning default config paths
- Keep environment variables higher priority than file values
- Preserve `internal/app.Config` and `ApplyDefaultsAndValidate()` as the runtime validation boundary

## Non-Goals

- Supporting legacy JSON or YAML config formats
- Adding dynamic config reload in this change
- Building a generic nested config abstraction for every future package
- Supporting partial in-place overrides of list elements

## Recommended Approach

Use `github.com/spf13/viper` in `cmd/wukongim/config.go` for file lookup and environment variable overlay, using Viper's `envfile` support for `KEY=value` parsing, but keep the final `app.Config` construction explicit.

Recommended responsibilities:

- `cmd/wukongim/main.go`
  - parse `-config`
  - call `loadConfig()`
  - start the app
- `cmd/wukongim/config.go`
  - choose the config source
  - load `.conf` values and environment variable overrides
  - parse strings and JSON list values into `app.Config`
- `internal/app/config.go`
  - remain format-agnostic
  - keep defaults and structural validation

This keeps the entry layer thin and avoids leaking Viper into the composition root.

## Config Contract

### File Name

The canonical file name is:

```text
wukongim.conf
```

### Key Format

Config file keys and environment variable keys must be identical and use the `WK_` prefix.

Examples:

```conf
WK_NODE_ID=1
WK_NODE_NAME=node-1
WK_NODE_DATA_DIR=./data/node1
WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000
WK_CLUSTER_FORWARD_TIMEOUT=2s
WK_API_LISTEN_ADDR=0.0.0.0:5001
```

This design intentionally avoids maintaining one naming scheme for files and another for environment variables.

### Scalar Values

Scalar fields use direct string values and are converted explicitly by the loader:

- bool via `strconv.ParseBool`
- integers via `strconv.ParseUint`, `strconv.Atoi`, or equivalent
- durations via `time.ParseDuration`
- strings as-is after trimming when appropriate

### List Values

List values are encoded as JSON strings stored in a single `KEY=value` entry.

Examples:

```conf
WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"},{"id":2,"addr":"127.0.0.1:7001"}]
WK_CLUSTER_GROUPS=[{"id":1,"peers":[1,2]}]
WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"0.0.0.0:5100","transport":"stdnet","protocol":"wkproto"}]
```

The same representation is used in environment variables:

```bash
export WK_CLUSTER_NODES='[{"id":1,"addr":"127.0.0.1:7000"}]'
```

The environment variable value replaces the whole list. No list element merge or deep merge is supported.

## Startup And Lookup Rules

### Explicit Config Path

Keep the `-config` flag.

Behavior:

- if `-config` is provided, only that path is read
- if the file does not exist or cannot be parsed, startup fails immediately
- explicit config path takes precedence over default file search paths
- environment variable overrides still apply after the file is read

### Default Config Search

If `-config` is not provided, the process searches these paths in order and uses the first existing file:

1. `./wukongim.conf`
2. `./conf/wukongim.conf`
3. `/etc/wukongim/wukongim.conf`

This order keeps local development convenient while still supporting a system-level install path similar to nginx.

### Startup Without Any Config File

If none of the default paths exist, startup may still continue if environment variables provide a complete valid configuration.

If both of these are true:

- no config file was found
- environment variables do not supply a valid complete config

startup fails and the error should include the attempted default file paths.

## Parsing Rules

The loader should not rely on implicit map-to-struct decoding for the entire config. It should read known keys explicitly and build `app.Config` directly.

Reasons:

- precise error messages for malformed scalar values
- explicit control over JSON list parsing
- no hidden coupling between Viper field names and `app.Config` field names
- easier future review when config semantics change

### Defaults

Code-level defaults remain only for values that are runtime defaults rather than deployment-specific topology:

- API listen address defaults to `0.0.0.0:5001`
- gateway listeners default to the current built-in listener set
- storage subpaths continue to be derived in `internal/app.Config.ApplyDefaultsAndValidate()`

Cluster topology remains explicit. Required cluster fields are not silently synthesized by the loader.

### Unknown Keys

Unknown keys in `.conf` or environment variables are ignored in this design.

The loader only parses known `WK_*` keys and fails on malformed values for those known keys. This keeps the loader simple and avoids building a second config-schema validation layer above `internal/app`.

### Error Messages

Errors should name the exact key being parsed.

Examples:

- `load config: parse WK_CLUSTER_FORWARD_TIMEOUT: ...`
- `load config: parse WK_CLUSTER_GROUPS as JSON: ...`
- `load config: no config file found in default paths: ...`

## File Format Scope

The mainline startup path supports only the `.conf` format described above.

Rules:

- the repository no longer documents JSON or YAML startup config
- `-config` can point to any file path, but its contents must follow `KEY=value`
- the repository adds one canonical sample file at the repository root: `wukongim.conf.example`

Because the system has not been released yet, the change should be done as a clean cutover rather than a staged migration.

## Package And File Changes

Expected change surface:

```text
cmd/wukongim/
  main.go
  config.go
  config_test.go
```

repository root:

```text
wukongim.conf.example
```

`internal/app/config.go` should remain focused on semantic defaults and validation. It should not import or depend on Viper.

## Testing Strategy

Primary coverage belongs in `cmd/wukongim/config_test.go`.

Required cases:

- parse a `.conf` file into `app.Config`
- use file defaults when optional keys are omitted
- prefer environment variables over file values
- accept startup with no config file when environment variables fully define the config
- fail when no default config file exists and environment variables are incomplete
- parse `WK_CLUSTER_NODES` and `WK_CLUSTER_GROUPS` from JSON strings
- fail with clear errors when list JSON is malformed
- fail with clear errors when duration values are malformed
- honor explicit `-config` over default-path discovery

The existing `internal/app` config validation continues to prove:

- cluster nodes are present
- cluster groups are present
- node id belongs to the configured single-node cluster or multi-node cluster
- gateway listeners and derived storage paths stay valid

## Rejected Alternatives

### Separate File Keys And Env Keys

Rejected because it creates two naming systems:

- file format such as `node.id=1`
- env format such as `WK_NODE_ID=1`

That makes docs, examples, and operational debugging drift over time.

### Indexed List Keys

Rejected format:

```conf
WK_CLUSTER_NODES_0_ID=1
WK_CLUSTER_NODES_0_ADDR=127.0.0.1:7000
```

This is mechanically parseable but too verbose and hard to maintain for human-authored cluster configs.

### Custom Delimited List DSL

Rejected examples:

```conf
WK_CLUSTER_NODES=1@127.0.0.1:7000,2@127.0.0.1:7001
WK_CLUSTER_GROUPS=1:1|2;2:2|3
```

This reduces readability only superficially and introduces bespoke parsing, escaping, and error-reporting rules that are harder to reason about than JSON.

## Success Criteria

- startup no longer requires a JSON config path
- `wukongim.conf` becomes the documented primary config format
- config files and environment variables share the same `WK_*` keys
- default path discovery works without `-config`
- environment variables override file values consistently
- list-valued config fields are represented as JSON strings and replace whole lists when overridden
