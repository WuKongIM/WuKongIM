# pkg/bench/model Flow

## Responsibility

`pkg/bench/model` owns shared wkbench schema, deterministic plan, report, rate,
and bench/v1 target API DTOs. Both the black-box `cmd/wkbench` implementation
and the promoted `cmd/wukongim` benchmark-only target surface may import this
package.

## Boundaries

- Keep this package as data model and lightweight parsing helpers only.
- Do not import `internal`, `internalv2`, `pkg/cluster`, `pkg/clusterv2`, or
  server runtime packages from here.
- Keep exported fields documented because these structs define config, YAML,
  JSON, and HTTP API contracts.
