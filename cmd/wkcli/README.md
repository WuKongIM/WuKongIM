# wkcli

`wkcli` is the extensible operations CLI for WuKongIM. The root package stays
thin: root wiring owns shared IO, exit-code handling, and subcommand
registration; each feature command lives in its own directory.

## Commands

```bash
go run ./cmd/wkcli <command> [flags]
```

Current placeholders:

| Command | Purpose |
| --- | --- |
| `context` | Manages named WuKongIM server API contexts. |
| `top` | Reserved for live WuKongIM runtime pressure inspection. |
| `bench` | Reserved for WuKongIM benchmark helpers. |

The `top` and `bench` placeholders are visible in help output and return a
stable `exitUnavailable` code until implemented.

## Contexts

Contexts store one or more WuKongIM HTTP API server addresses under a name. The
selected context becomes the default target for future commands that need server
addresses.

```bash
go run ./cmd/wkcli context add dev \
  --server http://127.0.0.1:5001 \
  --server http://127.0.0.1:5002 \
  --description "local two-node cluster" \
  --select

go run ./cmd/wkcli context ls
go run ./cmd/wkcli context show
go run ./cmd/wkcli context current
go run ./cmd/wkcli context select dev
go run ./cmd/wkcli context rm dev
```

`--server` can be repeated or given as a comma-separated list. Server addresses
must be absolute `http://` or `https://` API URLs.

By default, contexts are stored under the user config directory:

```text
<user-config-dir>/wukongim/wkcli/
  current_context
  contexts/
    dev.json
```

Use `--context-dir` to point tests or local experiments at an isolated store.

## Extending

To add a command:

1. Create `cmd/wkcli/internal/<name>/command.go`.
2. Add `func NewCommand(deps command.Deps) *cobra.Command`.
3. Register that factory in `defaultCommandFactories`.
4. Add focused tests in `cmd/wkcli/main_test.go` or a command-specific test
   file.

Use `command.Deps` for output streams instead of touching `os.Stdout` or
`os.Stderr` directly. Shared CLI primitives live under
`cmd/wkcli/internal/command` so subcommand packages stay small and explicit.
