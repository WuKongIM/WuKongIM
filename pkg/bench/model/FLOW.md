# pkg/bench/model Flow

## Responsibility

`pkg/bench/model` owns shared wkbench schema, deterministic plan, report, rate,
and bench/v1 target API DTOs. Both the black-box `cmd/wkbench` implementation
and the promoted `cmd/wukongim` benchmark-only target surface may import this
package.

## Boundaries

- Keep this package as data model and lightweight parsing helpers only.
- Do not import `internal`, `pkg/cluster`, or server runtime packages from
  here.
- Keep exported fields documented because these structs define config, YAML,
  JSON, and HTTP API contracts.

`Worker.Client` is an optional complete per-session capacity profile. When it
is present, every capacity is positive; when it is omitted, wkbench retains the
existing generic client defaults. The coordinator copies only the selected
worker's profile into that worker's assignment and never places worker control
credentials in the assignment payload.
