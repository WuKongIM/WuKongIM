# Plugin Protocol Compatibility

This package contains the legacy WuKongIM plugin protobuf schema and Go helper
methods used by Phase 1 plugin host RPC adapters.

The wire field numbers are intentionally kept compatible with plugins built
with `github.com/WuKongIM/go-pdk`. Keep runtime packages byte-oriented: only
usecase and access adapters should import this package.

Regenerate `plugin.pb.go` after schema edits with:

```sh
protoc --go_out=. --go_opt=paths=source_relative internal/usecase/plugin/pluginproto/plugin.proto
```
